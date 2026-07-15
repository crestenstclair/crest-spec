package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type initOptions struct {
	agent  string
	force  bool
	stdin  io.Reader
	stdout io.Writer
}

type initFile struct {
	path    string
	content string
}

func cmdInit(args []string) {
	opts := parseInitFlags(args)
	opts.stdin = os.Stdin
	opts.stdout = os.Stdout

	if err := runInit(opts); err != nil {
		fmt.Fprintf(os.Stderr, "init: %v\n", err)
		os.Exit(1)
	}
}

func parseInitFlags(args []string) initOptions {
	opts := initOptions{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			if i+1 < len(args) {
				opts.agent = args[i+1]
				i++
			}
		case "--force":
			opts.force = true
		}
	}
	return opts
}

func runInit(opts initOptions) error {
	if opts.stdin == nil {
		opts.stdin = os.Stdin
	}
	if opts.stdout == nil {
		opts.stdout = os.Stdout
	}

	agent := strings.TrimSpace(strings.ToLower(opts.agent))
	if agent == "" {
		selected, err := promptAgent(opts.stdin, opts.stdout)
		if err != nil {
			return err
		}
		agent = selected
	}

	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate crest-spec binary: %w", err)
	}

	files, err := buildInitFiles(agent, binaryPath)
	if err != nil {
		return err
	}
	for _, file := range files {
		if err := writeInitFile(file, opts.force); err != nil {
			return err
		}
		fmt.Fprintf(opts.stdout, "wrote %s\n", file.path)
	}
	fmt.Fprintln(opts.stdout, "restart your agent host so it reloads the new configuration")
	return nil
}

func promptAgent(stdin io.Reader, stdout io.Writer) (string, error) {
	fmt.Fprintln(stdout, "Which agent host should crest-spec initialize?")
	fmt.Fprintln(stdout, "  1) OpenCode")
	fmt.Fprintln(stdout, "  2) Claude Code")
	fmt.Fprintln(stdout, "  3) Both")
	fmt.Fprint(stdout, "Select [1]: ")

	scanner := bufio.NewScanner(stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "opencode", nil
	}

	switch strings.TrimSpace(strings.ToLower(scanner.Text())) {
	case "", "1", "open", "opencode", "open-code":
		return "opencode", nil
	case "2", "claude", "claude-code", "claude code":
		return "claude-code", nil
	case "3", "both", "all":
		return "both", nil
	default:
		return "", fmt.Errorf("unknown agent host %q", strings.TrimSpace(scanner.Text()))
	}
}

func buildInitFiles(agent, binaryPath string) ([]initFile, error) {
	switch agent {
	case "opencode":
		return opencodeInitFiles(binaryPath), nil
	case "claude-code":
		return claudeCodeInitFiles(binaryPath), nil
	case "both":
		files := opencodeInitFiles(binaryPath)
		files = append(files, claudeCodeInitFiles(binaryPath)...)
		return files, nil
	default:
		return nil, fmt.Errorf("unknown agent host %q", agent)
	}
}

func writeInitFile(file initFile, force bool) error {
	if !force {
		if _, err := os.Stat(file.path); err == nil {
			return fmt.Errorf("%s already exists; use --force to overwrite", file.path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(file.path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file.path, []byte(file.content), 0o644)
}

func opencodeInitFiles(binaryPath string) []initFile {
	return []initFile{
		{path: "opencode.json", content: opencodeConfig(binaryPath)},
		{path: ".opencode/agents/crest-orchestrator.md", content: crestOrchestratorAgent()},
		{path: ".opencode/agents/crest-generator.md", content: crestGeneratorAgent()},
		{path: ".opencode/agents/crest-triage.md", content: crestTriageAgent()},
		{path: ".opencode/agents/crest-verifier.md", content: crestVerifierAgent()},
		{path: ".opencode/agents/crest-design.md", content: crestDesignAgent()},
		{path: ".opencode/agents/crest-tasks.md", content: crestTasksAgent()},
		{path: ".opencode/commands/apply-crest-spec.md", content: applyCrestSpecCommand()},
	}
}

func claudeCodeInitFiles(binaryPath string) []initFile {
	return []initFile{
		{path: ".mcp.json", content: claudeMCPConfig(binaryPath)},
	}
}

func opencodeConfig(binaryPath string) string {
	return fmt.Sprintf(`{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "crest-spec": {
      "type": "local",
      "command": [%q],
      "cwd": ".",
      "environment": {
        "CREST_SPEC_SPEC_DIR": "./spec",
        "CREST_SPEC_GENERATE_MODEL": "opencode-stable"
      },
      "enabled": true,
      "timeout": 30000
    }
  },
  "agent": {
    "crest-orchestrator": {
      "mode": "primary"
    }
  }
}
`, binaryPath)
}

func claudeMCPConfig(binaryPath string) string {
	return fmt.Sprintf(`{
  "mcpServers": {
    "crest-spec": {
      "command": %q,
      "env": {
        "CREST_SPEC_SPEC_DIR": "./spec",
        "CREST_SPEC_GENERATE_MODEL": "claude-sonnet-5"
      }
    }
  }
}
`, binaryPath)
}

func crestOrchestratorAgent() string {
	return `---
description: Runs the crest-spec session loop using crest-spec MCP tools and OpenCode subagents.
mode: primary
model: anthropic/claude-sonnet-4-6
permission:
  task:
    "*": deny
    "crest-generator": allow
    "crest-verifier": allow
    "crest-triage": allow
    "crest-design": allow
    "crest-tasks": allow
  edit: ask
  bash: ask
---
You orchestrate crest-spec. crest-spec is a passive MCP state engine, not an agent runner.

Run the canonical loop:
1. Call spec/validate.
2. Call spec/plan and inspect whether the increment is expected.
3. Call spec/begin. If PendingDestroys is non-empty, stop and ask the user before spec/confirm_destroys.
4. Repeatedly call spec/next.
5. For each resource in the current wave, dispatch one crest-generator subagent concurrently and wait for the wave's subagents to return.
6. Reconcile outcomes against crest-spec state, using spec/sql when needed.
7. Run spec/verify_wave after committed resources.
8. If validation fails, dispatch crest-triage and call spec/resolve with concrete guidance.
9. Halt loudly on repeated non-convergence. Do not auto-skip.
10. Call spec/finish only when the engine permits it. Do not force without user approval.

Concurrency is required for speed: resources returned by the same spec/next wave are independent enough to generate in parallel. Dispatch all eligible current-wave resource agents together, then reconcile after they return. Do not start a second crest-spec session while a wave is running.

Never ask crest-spec to perform a task. Use its tools as state, prompt, commit, verification, and ledger operations.
`
}

func crestGeneratorAgent() string {
	return `---
description: Generates or updates one crest-spec resource through an immutable context and execution attempt.
mode: subagent
model: anthropic/claude-sonnet-4-6
permission:
  task: deny
  edit: allow
  bash: ask
---
You generate exactly one crest-spec resource.

Inputs from the orchestrator must include session_id, resource_id, model_label, and max_attempts.

Loop until committed or attempts are exhausted:
1. Call spec/context with the session_id and resource_id.
2. Treat Role, SystemPrompt, and Prompt as authoritative. Honor invariants as hard constraints.
3. Before generating, call spec/execution_start with protocol crest-execution-v1, AttemptID, ContextHash, TemplateHashes, SystemPrompt, host_name "opencode", the exact model/settings/tools/permissions, and an idempotency key derived from AttemptID.
4. If context contains previous files, Previous Errors, Guidance, or a role handoff, edit the previous attempt in the returned role. Do not restart from scratch.
5. Produce only the files requested by the prompt.
6. Call spec/commit with attempt_id, files, and a short note. Session, resource, and model are derived from SQLite.
7. If Committed is false, read Validations and FailureCategory, fetch fresh spec/context, and follow its corrective role handoff.

Return structured data: resource_id, outcome, attempts, error, files.
`
}

func crestTriageAgent() string {
	return `---
description: Attributes crest-spec validation failures to owning resources and records resolve guidance.
mode: subagent
model: anthropic/claude-sonnet-4-6
permission:
  task: deny
  edit: deny
  bash: ask
---
You triage crest-spec failures after a wave gate or resource failure.

Use the failure text, generated file ownership, spec/history, spec/inspect, and spec/sql to attribute each failure to the correct resource. Call spec/resolve with concrete guidance for fixable failures.

Call spec/skip only when the spec is contradictory. The skip reason must quote the contradictory declarations or name the missing dependency from the spec.

Never resolve a resource for a failure outside its owned files. Never treat difficulty or repeated failure as a skip reason.
`
}

func crestVerifierAgent() string {
	return `---
description: Triggers and inspects one crest-spec declared behavioral witness.
mode: subagent
model: anthropic/claude-sonnet-4-6
permission:
  task: deny
  edit: deny
  bash: deny
---
You verify exactly one declared crest-spec witness.

Inputs must include witness_id and may include session_id and a compatible legacy check_id.

Use spec/verification_definitions to inspect the immutable definition. Call spec/verify with witness_id, session_id, and optional check_id. crest-spec executes both declared commands, captures their provenance, and parses their observations; never supply observations yourself. Inspect the returned run with spec/verification_inspect. If a legacy check_id passed, call spec/graduate.

A malformed observation, command failure, source-tree mismatch, or negative case that passes is a verification failure, not a pass.

Return structured data: witness_id, validation_run_id, classification, reason, and legacy graduation status when applicable.
`
}

func crestDesignAgent() string {
	return `---
description: Derives a bounded-context observable contract for crest-spec behavioral verification.
mode: subagent
model: anthropic/claude-sonnet-4-6
permission:
  task: deny
  edit: deny
---
You derive an observable contract for one bounded context.

Inspect the context's resources with spec/inspect. Produce a contract JSON object with public surfaces and measurable behaviors. Include only externally observable behavior. Call spec/design_commit with context_name and contract_json.

Return structured data: context_name, committed, contract_json, error.
`
}

func crestTasksAgent() string {
	return `---
description: Decomposes a crest-spec context contract into resource-level tasks and typed behavioral checks.
mode: subagent
model: anthropic/claude-sonnet-4-6
permission:
  task: deny
  edit: deny
---
You author resource-level tasks and behavioral checks for one resource.

Inspect the resource with spec/inspect. Decompose the bounded-context contract into tasks this resource owns. Checks must use measurable typed predicate fields. Use numeric operators only for numeric fields, equals for booleans/strings/enums, and list operators only for arrays. Call spec/tasks_commit.

Return structured data: resource_id, committed, task_count, check_count, error.
`
}

func applyCrestSpecCommand() string {
	return `---
description: Apply the current crest-spec using the OpenCode native subagent loop.
agent: crest-orchestrator
---
Apply the current crest-spec using the canonical host-side loop from docs/AGENT_INTEGRATION.md.

User arguments: $ARGUMENTS

Use the crest-spec MCP tools. Validate, plan, begin, dispatch native OpenCode subagents concurrently per wave, commit through the engine gate, verify waves, resolve with guidance, and finish only when the engine allows it. Surface destructive changes, stalls, unresolved checks, and force/skip decisions to the user.
`
}
