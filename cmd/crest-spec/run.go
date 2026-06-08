package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func cmdRun(args []string) {
	flags := parseRunFlags(args)
	specDir := resolveSpecDir(flags.specDir)
	ensureMCPConfig(specDir, flags.model)
	prompt := buildRunPrompt()

	claudeArgs := []string{"--permission-mode", "bypassPermissions", prompt}
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		fmt.Fprintf(os.Stderr, "claude CLI not found in PATH\n")
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Starting crest-spec session (spec: %s)\n", specDir)
	if err := syscallExec(claudePath, append([]string{"claude"}, claudeArgs...), os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "exec claude: %v\n", err)
		os.Exit(1)
	}
}

type runFlags struct {
	specDir string
	model   string
}

func parseRunFlags(args []string) runFlags {
	f := runFlags{
		specDir: "./spec",
		model:   "claude-sonnet-4-6",
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--spec-dir":
			if i+1 < len(args) {
				f.specDir = args[i+1]
				i++
			}
		case "--model":
			if i+1 < len(args) {
				f.model = args[i+1]
				i++
			}
		}
	}
	return f
}

func resolveSpecDir(specDir string) string {
	abs, err := filepath.Abs(specDir)
	if err != nil {
		return specDir
	}
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "spec directory not found: %s\n", abs)
		fmt.Fprintf(os.Stderr, "Create it with: mkdir -p %s\n", specDir)
		os.Exit(1)
	}
	return abs
}

func ensureMCPConfig(specDir, model string) {
	mcpPath := ".mcp.json"
	if _, err := os.Stat(mcpPath); err == nil {
		return
	}

	selfPath, err := os.Executable()
	if err != nil {
		selfPath = "crest-spec"
	}

	config := map[string]any{
		"mcpServers": map[string]any{
			"crest-spec": map[string]any{
				"command": selfPath,
				"env": map[string]string{
					"CREST_SPEC_SPEC_DIR":         specDir,
					"CREST_SPEC_GENERATE_MODEL":   model,
					"CREST_SPEC_VERIFY_MODEL":     model,
					"CREST_SPEC_MAX_RETRIES":      "3",
					"CREST_SPEC_PERMISSION_MODE":  "bypassPermissions",
				},
			},
		},
	}

	data, _ := json.MarshalIndent(config, "", "  ")
	if err := os.WriteFile(mcpPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write .mcp.json: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Created .mcp.json (spec: %s, model: %s)\n", specDir, model)
}

func buildRunPrompt() string {
	return `You are driving a crest-spec code generation session.

You have crest-spec connected as an MCP server.

## Pipeline

1. **spec/plan** — see what needs generating
2. **spec/begin** — start session (returns session_id, plan, waves, pending destroys)
3. Handle destroys (if PendingDestroys is non-empty):
   - Review the list of resources pending deletion
   - **spec/confirm_destroys** (session_id, resource_ids) — confirm which to delete
4. **spec/run_wave** (session_id) — dispatch entire wave in parallel.
   Blocks until the wave completes and returns the full result inline.
   No job_id, no polling — the result comes back directly.
   Progress notifications stream as each resource finishes.
   Result contains:
   - committed: resources that succeeded
   - rejected: resources that failed validation (with error context)
   - errored: resources that failed generation
5. Handle failures:
   - **spec/resolve** — provide guidance, re-dispatch
   - **spec/amend** — fix CUE spec, re-dispatch
   - **spec/skip** — skip and move on
6. Repeat step 4 until done=true
7. **spec/finish** (session_id) — finalize session
8. **spec/deep_review** (session_id) — run SOLID/DI/clean code review

Use model_overrides in spec/run_wave to assign opus to complex resources
that need stronger reasoning. Sonnet is the default for all resources.

## Observability (check progress at any time)

- **spec/status** (session_id) — session overview: current wave, total waves,
  per-wave counts of committed/rejected/errored/pending resources.
- **spec/wave_status** (session_id, wave_index) — detailed per-resource view
  within a wave: state, attempts, max_retries, last_error.

## Single-resource re-dispatch

**spec/dispatch** (session_id, resource_id, model) — atomic generate-and-commit.
Blocks until complete and returns the result inline (no polling needed).
Useful for re-dispatching individual failed resources after providing guidance.

## Important

- Do NOT use shell commands to run crest-spec. Use the MCP tools exclusively.
- You are a DISPATCHER, not a code generator. Never write code yourself.
- spec/run_wave and spec/dispatch return results directly — no poll_result needed.
  Only run_prompt (manual pipeline) requires poll_result.
- Waves must be processed sequentially (wave N+1 depends on wave N).
- When a sub-agent raises an issue it cannot resolve, surface it to the user.

Work through every resource. Do not skip any unless generation truly fails.

Start now — call spec/plan to see what needs generating.`
}
