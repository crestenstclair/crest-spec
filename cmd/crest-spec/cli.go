package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/rs/zerolog"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/engine"
	specmod "github.com/crestenstclair/crest-spec/internal/spec"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func cliHelp() {
	fmt.Fprintln(os.Stderr, "crest-spec — declarative code generation")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Usage: crest-spec <command> [flags]")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  serve         start MCP server on stdio (default)")
	fmt.Fprintln(os.Stderr, "  plan          show what would change")
	fmt.Fprintln(os.Stderr, "  validate      check spec structural validity")
	fmt.Fprintln(os.Stderr, "  graph         dump resource dependency graph")
	fmt.Fprintln(os.Stderr, "  status        show current state")
	fmt.Fprintln(os.Stderr, "  unlock        force-clear stale lock")
	fmt.Fprintln(os.Stderr, "  dashboard     start web dashboard")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Code generation is driven by an agent through MCP tools.")
	fmt.Fprintln(os.Stderr, "Use 'serve' to start the MCP server, then orchestrate via:")
	fmt.Fprintln(os.Stderr, "  spec_begin → spec_next → spec_context → run_prompt → spec_commit → spec_finish")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  --target <id>       apply a single resource")
	fmt.Fprintln(os.Stderr, "  --model <model>     override generation model")
	fmt.Fprintln(os.Stderr, "  --force             force regeneration of all")
	fmt.Fprintln(os.Stderr, "  --incremental       wave-by-wave with verification")
	fmt.Fprintln(os.Stderr, "  --addr <host:port>  dashboard listen address (default: :8080)")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Environment:")
	config.Help()
}

type cliFlags struct {
	target      string
	model       string
	force       bool
	incremental bool
	addr        string
}

func parseFlags(args []string) cliFlags {
	var f cliFlags
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--target":
			if i+1 < len(args) {
				f.target = args[i+1]
				i++
			}
		case "--model":
			if i+1 < len(args) {
				f.model = args[i+1]
				i++
			}
		case "--force":
			f.force = true
		case "--incremental":
			f.incremental = true
		case "--addr":
			if i+1 < len(args) {
				f.addr = args[i+1]
				i++
			}
		}
	}
	return f
}

type cliContext struct {
	spec *specmod.Spec
	eng  *engine.Engine
	st   *store.Store
	cfg  *config.Config
	log  zerolog.Logger
}

func newCLIContext() (*cliContext, func(), error) {
	cfg, err := config.New()
	if err != nil {
		return nil, nil, fmt.Errorf("config: %w", err)
	}

	log := zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()

	dbPath := dbPath()
	st, err := store.New(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("store: %w", err)
	}

	st.CleanupOrphans(processAlive)

	ag := agent.New(cfg.AgentPath, cfg.APIKey, cfg.DefaultModel, cfg.PermissionMode, cfg.Timeout)
	eng := engine.New(ag, nil, cfg)
	sp := specmod.New(eng, st, specmod.OSFileSystem{}, cfg)

	cleanup := func() { st.Close() }
	return &cliContext{spec: sp, eng: eng, st: st, cfg: cfg, log: log}, cleanup, nil
}

func runCLI(command string, args []string) {
	flags := parseFlags(args)

	switch command {
	case "plan":
		cmdPlan(flags)
	case "apply":
		cmdApply(flags)
	case "validate":
		cmdValidate()
	case "graph":
		cmdGraph()
	case "status":
		cmdStatus()
	case "unlock":
		cmdUnlock()
	case "dashboard":
		cmdDashboard(flags)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", command)
		cliHelp()
		os.Exit(1)
	}
}

func cmdPlan(flags cliFlags) {
	cc, cleanup, err := newCLIContext()
	if err != nil {
		fatal(err)
	}
	defer cleanup()

	result, err := cc.spec.Plan(context.Background())
	if err != nil {
		fatal(err)
	}

	if len(result.Actions) == 0 {
		fmt.Println("No changes detected. Spec is up to date.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ACTION\tRESOURCE\tREASON")
	fmt.Fprintln(w, "------\t--------\t------")
	for _, a := range result.Actions {
		fmt.Fprintf(w, "%s\t%s\t%s\n", a.Kind, a.ResourceID, a.Reason)
	}
	w.Flush()

	fmt.Printf("\n%d resources across %d waves\n", len(result.Actions), len(result.Waves))
	for i, wave := range result.Waves {
		filtered := filterPlanWave(wave, result.Actions)
		if len(filtered) > 0 {
			fmt.Printf("  Wave %d: %s\n", i+1, strings.Join(filtered, ", "))
		}
	}
}

func filterPlanWave(wave []string, actions []specmod.PlanAction) []string {
	planSet := make(map[string]bool)
	for _, a := range actions {
		planSet[a.ResourceID] = true
	}
	var out []string
	for _, id := range wave {
		if planSet[id] {
			out = append(out, id)
		}
	}
	return out
}

func cmdApply(flags cliFlags) {
	fmt.Fprintln(os.Stderr, "The 'apply' command has been removed.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Code generation is driven by an agent through MCP tools:")
	fmt.Fprintln(os.Stderr, "  spec_begin → spec_next → spec_context → run_prompt → spec_commit → spec_finish")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Use 'crest-spec plan' to see what needs generating,")
	fmt.Fprintln(os.Stderr, "then let the agent orchestrate via MCP.")
	os.Exit(1)
}

func cmdValidate() {
	cc, cleanup, err := newCLIContext()
	if err != nil {
		fatal(err)
	}
	defer cleanup()

	result, err := cc.spec.Validate(context.Background())
	if err != nil {
		fatal(err)
	}

	if result.Valid {
		fmt.Printf("✓ Valid (%d resources)\n", result.ResourceCount)
	} else {
		fmt.Printf("✗ Invalid (%d errors):\n", len(result.Errors))
		for _, e := range result.Errors {
			fmt.Printf("  - %s\n", e)
		}
		os.Exit(1)
	}
}

func cmdGraph() {
	cc, cleanup, err := newCLIContext()
	if err != nil {
		fatal(err)
	}
	defer cleanup()

	result, err := cc.spec.GraphInfo(context.Background())
	if err != nil {
		fatal(err)
	}

	fmt.Printf("Nodes (%d):\n", len(result.Nodes))
	for _, n := range result.Nodes {
		fmt.Printf("  %s\n", n)
	}

	fmt.Printf("\nWaves (%d):\n", len(result.Waves))
	for i, wave := range result.Waves {
		fmt.Printf("  Wave %d: %s\n", i+1, strings.Join(wave, ", "))
	}
}

func cmdStatus() {
	cc, cleanup, err := newCLIContext()
	if err != nil {
		fatal(err)
	}
	defer cleanup()

	result, err := cc.spec.Status(context.Background())
	if err != nil {
		fatal(err)
	}

	b, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(b))
}

func cmdUnlock() {
	cc, cleanup, err := newCLIContext()
	if err != nil {
		fatal(err)
	}
	defer cleanup()

	if err := cc.spec.Unlock(context.Background()); err != nil {
		fatal(err)
	}
	fmt.Println("Lock released.")
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
