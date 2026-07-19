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
	if agent == "codex" || agent == "all" {
		fmt.Fprintln(opts.stdout, "Codex: trust this repository, then start a new task and say: apply this crest-spec")
	}
	fmt.Fprintln(opts.stdout, "restart your agent host so it reloads the new configuration")
	return nil
}

func promptAgent(stdin io.Reader, stdout io.Writer) (string, error) {
	fmt.Fprintln(stdout, "Which agent host should crest-spec initialize?")
	fmt.Fprintln(stdout, "  1) OpenCode")
	fmt.Fprintln(stdout, "  2) Claude Code")
	fmt.Fprintln(stdout, "  3) Codex")
	fmt.Fprintln(stdout, "  4) All")
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
	case "3", "codex", "openai-codex", "openai codex":
		return "codex", nil
	case "both":
		return "both", nil
	case "4", "all":
		return "all", nil
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
	case "codex":
		return codexInitFiles(binaryPath), nil
	case "both":
		files := opencodeInitFiles(binaryPath)
		files = append(files, claudeCodeInitFiles(binaryPath)...)
		return files, nil
	case "all":
		files := opencodeInitFiles(binaryPath)
		files = append(files, claudeCodeInitFiles(binaryPath)...)
		files = append(files, codexInitFiles(binaryPath)...)
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
		{path: ".claude/skills/apply-crest-spec/SKILL.md", content: claudeApplySkill()},
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
        "CREST_SPEC_GENERATE_MODEL": "opencode-stable",
        "CREST_SPEC_DISPATCH_TIMEOUT": "2m"
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
        "CREST_SPEC_GENERATE_MODEL": "claude-sonnet-5",
        "CREST_SPEC_DISPATCH_TIMEOUT": "2m"
      }
    }
  }
}
`, binaryPath)
}

func claudeApplySkill() string {
	return `---
name: apply-crest-spec
description: Apply or reconcile this project's crest-spec with a time-first, governed worker pool.
---

# Apply crest-spec with Claude Code

CrestSpec is the passive state and validation engine. Claude Code owns all LLM dispatch; never ask CrestSpec to spawn agents or write implementation code outside spec/commit.

1. Run spec/validate, spec/plan, then spec/begin. Stop for explicit approval of any PendingDestroys.
2. Create a bounded pool of generator subagents once and retain their identities. Reassign idle workers sequential resources across dependency waves instead of starting a fresh agent for every resource.
3. Whenever workers are idle, call spec/context_batch with limit equal to the currently idle worker slots. It prepares at most that many immutable contexts but does not claim them. Repeated calls refill the pool; never prefetch beyond immediate capacity. Report per-item errors and blocked contexts without dispatching them. Choose an idle worker for each unblocked item, call spec/dispatch_event dispatch_requested with its DispatchID and stable, nonempty worker_id and host_session_id values, and give the item as prepared_context to that worker only after the event succeeds. If dispatch returns rebuild_context, do not assign the abandoned stale attempt; refill the slot through context_batch. This refresh does not consume a generation retry. The worker immediately records worker_acknowledged with the same identities. If assignment delivery or the worker fails before execution registration, call dispatch_failed with those exact identities and a concrete reason so the claim is released immediately without consuming a retry. A prepared initial attempt must then go directly to spec/execution_start; do not call spec/context again for it.
4. Every assignment registers its own actual host/model/tools/settings/context identity through crest-execution-v1 and submits only through spec/commit. For an UPDATE-mode retry, the worker calls spec/context, then records dispatch_requested for that new DispatchID before worker_acknowledged and spec/execution_start. If dispatch or execution_start returns rebuild_context, the worker calls spec/context again and dispatches the replacement without charging a model retry. Lifecycle calls are mandatory; stop the assignment on any other lifecycle error and report dispatch_failed when no execution was registered rather than leaving a claim or relying on implicit dispatch.
5. Reconcile the complete wave from CrestSpec state and call spec/verify_wave. Deferred validation advances to the next context_batch.
6. When context_batch returns validation_ready=true, call spec/verify_wave with validation_wave_index even if there are no contexts. Optimistic or reused resources require no dummy worker but still require closure validation.
7. Attribute a failed closure gate to its owners, call spec/resolve with concrete guidance, and reuse the existing workers for repairs. Halt loudly at retry limits; never auto-skip or force finish.
8. Run required declared witnesses through spec/verify and call spec/finish without force.

Worker count is bounded by host capacity and current wave width. A worker processes only one assignment at a time and clears all resource-specific context before reuse. CrestSpec never owns or counts the worker processes; its immutable dispatch, attempt, and execution manifests are the source of truth.
`
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
4. Create a bounded set of crest-generator subagents and retain their identities for reuse across waves.
5. Whenever the pool has idle workers, call spec/context_batch with limit equal to the number of idle worker slots. It prepares at most that many contexts without claiming them; repeated calls refill newly idle slots. Never prefetch contexts beyond immediate capacity. Report errors and blocked contexts without assigning them. Choose an idle worker for every unblocked returned context, record spec/dispatch_event dispatch_requested with its DispatchID and stable, nonempty worker_id and host_session_id values, then assign it only after that event succeeds; reuse workers for later resources only after their prior assignments are terminal. If dispatch returns rebuild_context, do not assign that stale abandoned attempt; refill the slot through context_batch without charging a generation retry. The worker must record worker_acknowledged before spec/execution_start. If delivery or a worker fails before execution registration, call dispatch_failed with the exact identities and reason, then refill the released slot. CrestSpec rejects overlapping assignments.
6. Reconcile outcomes against crest-spec state, using spec/sql when needed.
7. Run spec/verify_wave after committed resources.
8. If validation fails, dispatch crest-triage and call spec/resolve with concrete guidance.
9. Halt loudly on repeated non-convergence. Do not auto-skip.
10. If context_batch returns validation_ready=true, call spec/verify_wave with validation_wave_index even when there were no generator assignments. Then call spec/finish only when the engine permits it. Do not force without user approval.

Concurrency is required for speed: assign eligible contexts from each capacity-limited spec/context_batch concurrently. A prepared context is not a claim or worker reservation, so prepare only what idle workers can start promptly. Keep one assignment per worker, account for every returned context, and preserve a distinct execution identity for every attempt. Treat a missing DispatchID or failed dispatch lifecycle event as a hard orchestration error; never use implicit dispatch. Do not start a second crest-spec session while a wave is running.

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
You generate exactly one assigned crest-spec resource at a time. The orchestrator may reuse this worker for a later assignment; clear resource-specific context between assignments.

Inputs from the orchestrator must include session_id, resource_id, prepared_context when available, worker_id, host_session_id, model_label, and max_attempts. Before assigning a prepared_context, the orchestrator must successfully record dispatch_requested for it.

Loop until committed or attempts are exhausted:
1. Use prepared_context for the initial attempt when supplied. Otherwise call spec/context with the session_id and resource_id. Never request a duplicate initial context for a prepared attempt.
2. If the context has NoDispatch=true, return candidate_reinstated without recording dispatch or acknowledgement and have the parent call spec/next. If it has Blocked=true, return context_blocked without dispatch.
3. For an initial prepared_context, the orchestrator already recorded dispatch_requested. For any context this worker fetched through spec/context, including every retry, call spec/dispatch_event dispatch_requested with its DispatchID, worker_id, and host_session_id. Then call spec/dispatch_event worker_acknowledged with the same DispatchID and identities. If dispatch_requested returns rebuild_context for a worker-fetched context, do not acknowledge or start that stale abandoned attempt; call spec/context again and dispatch its new DispatchID without charging a generation retry. If a live failure prevents execution registration after claim, call dispatch_failed with the exact identities and a concrete reason before stopping; implicit dispatch is forbidden.
4. Treat Role, SystemPrompt, and Prompt as authoritative. Honor invariants as hard constraints. Before generating, call spec/execution_start with protocol crest-execution-v1, AttemptID, ContextHash, TemplateHashes, SystemPrompt, host_name "opencode", the exact model/settings/tools/permissions, and an idempotency key derived from AttemptID. If execution_start returns rebuild_context, fetch a new spec/context and repeat dispatch_requested then worker_acknowledged before starting it, without charging a generation retry. For another start rejection that registered no execution, call dispatch_failed before returning.
5. If context contains previous files, Previous Errors, Guidance, or a role handoff, edit the previous attempt in the returned role. Do not restart from scratch.
6. Produce only the files requested by the prompt.
7. Call spec/commit with attempt_id, files, and a short note. Session, resource, and model are derived from SQLite.
8. If Committed is false, read Validations and FailureCategory, fetch fresh spec/context, and repeat the dispatch-request/acknowledgement lifecycle for that new DispatchID before following its corrective role handoff.

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

Use the crest-spec MCP tools. Validate, plan, begin, and refill the bounded reusable OpenCode worker pool by calling spec/context_batch with limit equal to currently idle worker slots. Explicitly record dispatch_requested before assigning each unclaimed prepared context. Require worker_acknowledged before execution_start, including a fresh dispatch request for every retry context. If assignment delivery or a worker fails before execution registration, immediately report dispatch_failed with the exact identities and reason so CrestSpec releases the claim without consuming a retry. Commit through the engine gate, verify waves including validation-ready waves with no generator work, resolve with guidance, and finish only when the engine allows it. Preserve a distinct execution identity for every worker assignment; never use implicit dispatch. Surface destructive changes, stalls, unresolved checks, and force/skip decisions to the user.
`
}
