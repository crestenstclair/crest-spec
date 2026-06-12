package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/mcp"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	if showHelp() {
		return
	}
	if runSubcommand() {
		return
	}
	runServer()
}

func showHelp() bool {
	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" {
			fmt.Fprintln(os.Stderr, "crest-spec — declarative code generation MCP server")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "Usage: crest-spec [command]")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "With no arguments, starts the MCP server on stdio.")
			fmt.Fprintln(os.Stderr, "Generation is driven by Claude Code via MCP — see the spec-generate skill.")
			fmt.Fprintln(os.Stderr, "Or connect via MCP: spec/begin → spec/next → spec/context → spec/commit → spec/finish")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "Commands:")
			fmt.Fprintln(os.Stderr, "  dashboard [--addr :8080]       start web dashboard for monitoring sessions")
			fmt.Fprintln(os.Stderr, "  state list                     print all resources in state")
			fmt.Fprintln(os.Stderr, "  state rm <resourceId>          remove a resource from state")
			fmt.Fprintln(os.Stderr, "  diff <apply_a> <apply_b>       show changes between two applies")
			fmt.Fprintln(os.Stderr, "  vacuum --before <date>         compact history older than date")
			fmt.Fprintln(os.Stderr, "  sql <query>                    execute a read-only SQL query")
			fmt.Fprintln(os.Stderr)
			config.Help()
			os.Exit(0)
		}
	}
	return false
}

func runSubcommand() bool {
	if len(os.Args) < 2 {
		return false
	}
	switch os.Args[1] {
	case "dashboard":
		flags := parseFlags(os.Args[2:])
		cmdDashboard(flags)
	case "state":
		if len(os.Args) >= 3 {
			switch os.Args[2] {
			case "list":
				cmdStateList()
				return true
			case "rm":
				if len(os.Args) < 4 {
					fmt.Fprintf(os.Stderr, "usage: crest-spec state rm <resourceId>\n")
					os.Exit(1)
				}
				cmdStateRm(os.Args[3])
				return true
			}
		}
		fmt.Fprintf(os.Stderr, "usage: crest-spec state [list|rm <resourceId>]\n")
		os.Exit(1)
	case "diff":
		if len(os.Args) < 4 {
			fmt.Fprintf(os.Stderr, "usage: crest-spec diff <apply_a> <apply_b>\n")
			os.Exit(1)
		}
		cmdDiff(os.Args[2], os.Args[3])
	case "vacuum":
		cmdVacuum(os.Args[2:])
	case "sql":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "usage: crest-spec sql <query>\n")
			os.Exit(1)
		}
		cmdSQL(strings.Join(os.Args[2:], " "))
	default:
		return false
	}
	return true
}

func runServer() {
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

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	sp := specmod.New(s, specmod.OSFileSystem{}, cfg)
	srv := mcp.New(sp, os.Stdin, os.Stdout, log.Logger, cfg)

	if cfg.HTTPAddr != "" {
		startHTTPTransport(cfg.HTTPAddr, srv, ctx)
	}

	log.Info().Str("db", dbPath).Msg("crest-spec ready")
	if err := srv.Run(ctx); err != nil {
		log.Error().Err(err).Msg("server error")
	}
	log.Info().Msg("shutting down")
}

func startHTTPTransport(addr string, srv *mcp.Server, ctx context.Context) {
	httpMux := http.NewServeMux()
	httpMux.HandleFunc("POST /mcp", srv.ServeHTTP)
	httpSrv := &http.Server{Addr: addr, Handler: httpMux}
	go func() {
		log.Info().Str("addr", addr).Msg("HTTP transport started")
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

func dbPath() string {
	dir := filepath.Join(".", ".crest-spec")
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "state.db")
}

// ---------------------------------------------------------------------------
// CLI subcommands
// ---------------------------------------------------------------------------

func openStore() *store.Store {
	s, err := store.New(dbPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		os.Exit(1)
	}
	return s
}

func cmdStateList() {
	s := openStore()
	defer s.Close()

	resources, err := s.ListResources()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list resources: %v\n", err)
		os.Exit(1)
	}

	if len(resources) == 0 {
		fmt.Println("No resources in state.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tCONTEXT\tDECL_HASH\tEFF_HASH\tMODEL\tSETTLED_AT")
	for _, r := range resources {
		settled := r.SettledAt.Format(time.RFC3339)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, r.Kind, r.ContextName,
			truncHash(r.DeclarationHash), truncHash(r.EffectiveHash),
			r.Model, settled)
	}
	w.Flush()
}

func truncHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

func cmdStateRm(resourceID string) {
	s := openStore()
	defer s.Close()

	if err := s.DeleteGeneratedFiles(resourceID); err != nil {
		fmt.Fprintf(os.Stderr, "delete generated files: %v\n", err)
		os.Exit(1)
	}
	if err := s.DeleteDependencies(resourceID); err != nil {
		fmt.Fprintf(os.Stderr, "delete dependencies: %v\n", err)
		os.Exit(1)
	}
	if err := s.DeleteResource(resourceID); err != nil {
		fmt.Fprintf(os.Stderr, "delete resource: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Removed resource %s from state.\n", resourceID)
}

func cmdDiff(applyA, applyB string) {
	s := openStore()
	defer s.Close()

	actionsA, err := s.ListApplyActions(applyA)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list actions for %s: %v\n", applyA, err)
		os.Exit(1)
	}
	actionsB, err := s.ListApplyActions(applyB)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list actions for %s: %v\n", applyB, err)
		os.Exit(1)
	}

	// Build maps of resourceID -> action for each apply
	mapA := make(map[string]string)
	for _, a := range actionsA {
		mapA[a.ResourceID] = a.Action
	}
	mapB := make(map[string]string)
	for _, a := range actionsB {
		mapB[a.ResourceID] = a.Action
	}

	// Collect all resource IDs
	allIDs := make(map[string]bool)
	for id := range mapA {
		allIDs[id] = true
	}
	for id := range mapB {
		allIDs[id] = true
	}

	if len(allIDs) == 0 {
		fmt.Println("No actions found in either apply.")
		return
	}

	fmt.Printf("Diff: %s vs %s\n\n", applyA, applyB)

	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "RESOURCE\tAPPLY_A\tAPPLY_B")
	for id := range allIDs {
		actionA := mapA[id]
		actionB := mapB[id]
		if actionA == "" {
			actionA = "-"
		}
		if actionB == "" {
			actionB = "-"
		}
		if actionA != actionB {
			fmt.Fprintf(w, "%s\t%s\t%s\n", id, actionA, actionB)
		}
	}
	w.Flush()
}

func cmdVacuum(args []string) {
	var dateStr string
	for i := 0; i < len(args); i++ {
		if args[i] == "--before" && i+1 < len(args) {
			dateStr = args[i+1]
			i++
		}
	}
	if dateStr == "" {
		fmt.Fprintf(os.Stderr, "usage: crest-spec vacuum --before <date>\n")
		fmt.Fprintf(os.Stderr, "  date format: YYYY-MM-DD or RFC3339\n")
		os.Exit(1)
	}

	before, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		// Try date-only format
		before, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid date %q: use YYYY-MM-DD or RFC3339 format\n", dateStr)
			os.Exit(1)
		}
	}

	s := openStore()
	defer s.Close()

	count, err := s.Vacuum(before)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vacuum: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Vacuumed %d records older than %s.\n", count, before.Format(time.RFC3339))
}

func cmdSQL(query string) {
	s := openStore()
	defer s.Close()

	results, err := s.ReadOnlyQuery(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sql: %v\n", err)
		os.Exit(1)
	}

	if len(results) == 0 {
		fmt.Println("No rows returned.")
		return
	}

	// Collect column names in stable order from first row
	var cols []string
	for col := range results[0] {
		cols = append(cols, col)
	}
	// Sort for deterministic output
	sort.Strings(cols)

	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, strings.Join(cols, "\t"))
	for _, row := range results {
		vals := make([]string, len(cols))
		for i, col := range cols {
			vals[i] = fmt.Sprintf("%v", row[col])
		}
		fmt.Fprintln(w, strings.Join(vals, "\t"))
	}
	w.Flush()
}
