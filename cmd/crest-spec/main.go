package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/engine"
	"github.com/crestenstclair/crest-spec/internal/mcp"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	if len(os.Args) >= 4 && os.Args[1] == "check" && os.Args[2] == "job" {
		checkJob(os.Args[3])
		return
	}

	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" {
			cliHelp()
			os.Exit(0)
		}
	}

	if len(os.Args) >= 2 {
		cmd := os.Args[1]
		switch cmd {
		case "plan", "apply", "validate", "graph", "status", "unlock":
			runCLI(cmd, os.Args[2:])
			return
		case "serve":
			// fall through to MCP server mode
		}
	}

	cfg, err := config.New()
	if err != nil {
		config.Help()
		panic(fmt.Sprintf("config: %v", err))
	}

	dbPath := dbPath()
	s, err := store.New(dbPath)
	if err != nil {
		panic(fmt.Sprintf("store: %v", err))
	}
	defer s.Close()

	cleaned, err := s.CleanupOrphans(processAlive)
	if err != nil {
		log.Warn().Err(err).Msg("orphan cleanup failed")
	} else if cleaned > 0 {
		log.Info().Int("cleaned", cleaned).Msg("cleaned orphaned jobs")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ag := agent.New(
		cfg.AgentPath,
		cfg.APIKey,
		cfg.DefaultModel,
		cfg.PermissionMode,
		cfg.Timeout,
	)

	eng := engine.New(ag, nil, cfg)

	sp := specmod.New(eng, s, specmod.OSFileSystem{}, cfg)

	srv := mcp.New(sp, eng, s, mcp.OSProcessTree{}, os.Stdin, os.Stdout, log.Logger, cfg)

	if cfg.HTTPAddr != "" {
		httpMux := http.NewServeMux()
		httpMux.HandleFunc("POST /mcp", srv.ServeHTTP)
		httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: httpMux}
		go func() {
			log.Info().Str("addr", cfg.HTTPAddr).Msg("HTTP transport started")
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error().Err(err).Msg("HTTP server error")
			}
		}()
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			httpSrv.Shutdown(shutdownCtx)
		}()
	}

	log.Info().Str("db", dbPath).Msg("crest-spec ready")
	if err := srv.Run(ctx); err != nil {
		log.Error().Err(err).Msg("server error")
	}
	log.Info().Msg("shutting down")
}

func checkJob(id string) {
	dbPath := dbPath()
	s, err := store.New(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	s.CleanupOrphans(processAlive)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	job, err := s.WaitForCompletion(ctx, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wait: %v\n", err)
		os.Exit(1)
	}

	switch job.Status {
	case "completed":
		fmt.Println(job.Result)
		s.DeleteJob(id)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "job %s: status=%s error=%s\n", id, job.Status, job.Error)
		s.DeleteJob(id)
		os.Exit(1)
	}
}

func dbPath() string {
	dir := filepath.Join(".", ".crest-spec")
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "state.db")
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}
