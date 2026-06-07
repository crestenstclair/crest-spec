package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
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
			config.Help()
			os.Exit(0)
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

	_ = agent.New(
		cfg.AgentPath,
		cfg.APIKey,
		cfg.DefaultModel,
		cfg.PermissionMode,
		cfg.Timeout,
	)

	log.Info().Str("db", dbPath).Msg("crest-spec ready (engine/mcp not yet wired)")
	<-ctx.Done()
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
