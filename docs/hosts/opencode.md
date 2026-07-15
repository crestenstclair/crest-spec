# OpenCode adapter for crest-spec

This adapter describes how to run the crest-spec generation loop from OpenCode while keeping crest-spec host-agnostic.

## Control model

OpenCode is the agent host and orchestrator. crest-spec is a passive MCP state engine. The OpenCode main agent runs the loop, dispatches OpenCode subagents, and makes judgement calls visible to the user. crest-spec never receives a natural-language task to fulfill.

The practical mapping is:

| Responsibility | OpenCode surface |
|---|---|
| Connect to crest-spec tools | `mcp` config in `opencode.json` |
| Run the top-level loop | project command or skill |
| Generate one resource | OpenCode `subagent`; dispatch all current-wave resources concurrently |
| Verify one behavioral check | OpenCode `subagent`; dispatch independent checks concurrently |
| Attribute failures | OpenCode `subagent` |
| Enforce who may dispatch | `permission.task` |

## MCP configuration

For interactive use, prefer local stdio MCP. Place this in the target project's `opencode.json` and adjust the binary path if needed:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "crest-spec": {
      "type": "local",
      "command": ["/absolute/path/to/crest-spec"],
      "cwd": ".",
      "environment": {
        "CREST_SPEC_SPEC_DIR": "./spec",
        "CREST_SPEC_GENERATE_MODEL": "opencode-stable"
      },
      "enabled": true,
      "timeout": 30000
    }
  }
}
```

Keep `CREST_SPEC_GENERATE_MODEL` stable for the project. It is a label recorded in state and mixed into hashes; it does not choose the OpenCode model.

For high fan-out scripts, HTTP is also available:

```bash
CREST_SPEC_SPEC_DIR=./spec \
CREST_SPEC_HTTP_ADDR=127.0.0.1:8792 \
CREST_SPEC_GENERATE_MODEL=opencode-stable \
/absolute/path/to/crest-spec
```

HTTP clients call `POST /mcp` with JSON-RPC `tools/call`. Native OpenCode agents should generally use the configured MCP tools directly unless a script needs plain `curl` fan-out.

## Recommended OpenCode agents

Create these as project agents under `.opencode/agents/`. They are templates; choose real model IDs from `opencode models`.

### `crest-orchestrator.md`

```markdown
---
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
```

### `crest-generator.md`

```markdown
---
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
3. Call spec/execution_start with crest-execution-v1 and the exact attempt, context/template hashes, host/model/settings/tools/permissions, and system instructions.
4. If context contains previous files, Previous Errors, Guidance, or a role handoff, edit the previous attempt in the returned role. Do not restart from scratch.
5. Produce only the files requested by the prompt.
6. Call spec/commit with attempt_id, files, and a short note.
7. If Committed is false, read Validations and FailureCategory, fetch fresh spec/context, and follow the routed role handoff.

Return structured data: resource_id, outcome, attempts, error, files.
```

### `crest-triage.md`

```markdown
---
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
```

### `crest-verifier.md`

```markdown
---
description: Verifies one crest-spec behavioral check with a real witness and degenerate stub.
mode: subagent
model: anthropic/claude-sonnet-4-6
permission:
  task: deny
  edit: allow
  bash: allow
  external_directory:
    "*": ask
    "/tmp/**": allow
---
You verify exactly one crest-spec behavioral check.

Inputs must include resource_id, check_id, behavior, and check_json.

Use spec/inspect to understand the public interface. Do not read committed implementation files for the behavior under test. Create a real witness and a degenerate stub baseline outside the project tree, preferably under /tmp. Run both. Extract typed observations. Call spec/verify with both real_observation and stub_observation. If spec/verify passes, call spec/graduate. Clean up temporary harnesses.

A missing observation, missing stub field, type mismatch, or stub that passes is a verification failure, not a pass.

Return structured data: check_id, passed, theater, reason, graduated.
```

### `crest-design.md`

```markdown
---
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
```

### `crest-tasks.md`

```markdown
---
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
```

## Recommended command

Create `.opencode/commands/apply-crest-spec.md`:

```markdown
---
description: Apply the current crest-spec using the OpenCode native subagent loop.
agent: crest-orchestrator
---
Apply the current crest-spec using the canonical host-side loop from docs/AGENT_INTEGRATION.md.

User arguments: $ARGUMENTS

Use the crest-spec MCP tools. Validate, plan, begin, dispatch native OpenCode subagents per wave, commit through the engine gate, verify waves, resolve with guidance, and finish only when the engine allows it. Surface destructive changes, stalls, unresolved checks, and force/skip decisions to the user.
```

## Permission notes

The orchestrator should be the only agent allowed to use the Task tool. Generator, verifier, triage, design, and task subagents should deny `task`.

MCP tools are exposed with the server name as a prefix in OpenCode. If the tool list becomes too large, disable broad access globally and allow the crest-spec MCP tools only on the crest agents.

## Model notes

OpenCode model selection is host-side. crest-spec's `model` argument and `CREST_SPEC_GENERATE_MODEL` are stable labels for state and hashes. Do not use them as the actual LLM selector.

A practical setup is:

| Agent | Model class |
|---|---|
| `crest-orchestrator` | strong reasoning |
| `crest-generator` | strongest coding model available |
| `crest-verifier` | strong reasoning/coding |
| `crest-triage` | strong reasoning, can be cheaper than generator |
| `crest-design`, `crest-tasks` | strong reasoning |

OpenRouter may be used as an OpenCode provider/model source, but it is not part of the crest-spec engine contract.

## Acceptance checklist

The adapter is working when a user can start OpenCode in the project, ask it to apply the current crest-spec, and observe that the main OpenCode agent:

- calls `spec/validate`, `spec/plan`, and `spec/begin`;
- stops for human approval before `spec/confirm_destroys`;
- calls `spec/next` and dispatches current-wave resource subagents concurrently;
- generator subagents call `spec/context`, `spec/execution_start`, and
  `spec/commit` themselves;
- retries use UPDATE-mode context rather than blank-slate generation;
- wave failures are attributed and resolved with `spec/resolve`;
- outcomes are reconciled against engine state, not subagent self-report;
- `spec/finish` is called only when blockers are resolved or the user explicitly approves force.
