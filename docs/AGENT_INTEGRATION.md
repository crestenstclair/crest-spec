# How an agent integrates with crest-spec

This document defines the contract between crest-spec and an agent host
(Claude Code, OpenCode, or any agentic CLI). Read the first section twice —
getting the direction of control wrong is the single most common integration
failure.

## The model, in one paragraph

**crest-spec is a passive state engine served over MCP. Your agent is the
orchestrator.** The engine never calls an LLM, interprets a natural-language
task, or dispatches sub-agents. It only launches validation and witness
commands explicitly declared in CUE. It holds
the spec-derived plan, session state, per-resource prompts, the commit
validation gate, and the verification/evidence ledger — and it answers tool calls.
Everything that *thinks* or *generates* is an agent on YOUR side of the MCP
boundary, spawned by YOUR host's native sub-agent mechanism.

### The wrong model (seen in the wild, does not work)

> "Once clients are connected to crest-spec's MCP, the host's agents would be
> dispatched *through* crest-spec's MCP. crest-spec would receive the task
> request, interpret it, and delegate to its defined sub-agents/workflows."

This is **backwards on every point**. crest-spec has no sub-agents, no
workflow runtime, and no task interpreter. If you send it a task description
and wait for it to "fulfill" the task, nothing happens — there is no tool
that accepts a task. The host dispatches its OWN sub-agents (one per
resource), and those sub-agents call crest-spec tools to fetch their prompt
and submit their output through the gate.

```
┌──────────────────────────────────────────────┐
│ Agent host (Claude Code / OpenCode / ...)    │
│                                              │
│  Orchestrator loop (a workflow/script YOU    │
│  run in the host)                            │
│    │  spawns N native sub-agents per wave    │
│    ▼                                         │
│  [generator agent] [generator agent] ...     │
│        │   ▲              │   ▲              │
└────────┼───┼──────────────┼───┼──────────────┘
   tool  │   │ context/     │   │  commit
   calls ▼   │ prompts      ▼   │  verdicts
┌──────────────────────────────────────────────┐
│ crest-spec (MCP server, pure state engine)   │
│  CUE spec → registry → plan/waves            │
│  SQLite: sessions, contexts, executions,     │
│          validations, evidence, learnings    │
│  Validation/witness gate (declared commands) │
└──────────────────────────────────────────────┘
```

The only commands crest-spec runs are those declared in the specification:
commit/wave validations (`cargo test`, `make demo-...`) and controlled
behavioral witnesses. These are deterministic verification commands, not
agents or LLM calls.

## Division of responsibilities

| Concern | crest-spec (engine) | Agent host (you) |
|---|---|---|
| Parse/validate the CUE spec | ✅ | |
| Compute plan, dependency waves, hashes | ✅ | |
| Serve and persist goal-directed per-resource context | ✅ | |
| Validate the execution protocol, roles, candidates, and failure routes | ✅ | |
| Run validation commands and accept/reject commits | ✅ | |
| Execute declared real/negative witnesses and persist evidence | ✅ | |
| Persist sessions across crashes/restarts | ✅ | |
| Decide WHEN to run a session | | ✅ |
| Spawn generator/verifier sub-agents | | ✅ (host-native mechanism) |
| Call the LLM that writes code | | ✅ (inside your sub-agents) |
| Retry/triage/halt policy | | ✅ (your orchestrator loop) |
| Author/edit the CUE spec | | ✅ (usually the top-level agent, with the human) |

## Transports

**stdio MCP** (default): the binary speaks MCP on stdin/stdout. Register it
in the host's MCP config (Claude Code: `.mcp.json`), e.g.:

```json
{ "mcpServers": { "crest-spec": {
    "command": "/path/to/crest-spec",
    "env": { "CREST_SPEC_SPEC_DIR": "./spec" } } } }
```

**HTTP** (for orchestrator scripts and fan-out): set `CREST_SPEC_HTTP_ADDR`
and the same tools are served as JSON-RPC over `POST /mcp`:

```bash
CREST_SPEC_SPEC_DIR=./spec CREST_SPEC_HTTP_ADDR=127.0.0.1:8792 \
  CREST_SPEC_GENERATE_MODEL=claude-sonnet-5 ./crest-spec
```

```bash
curl -s -X POST http://127.0.0.1:8792/mcp -H 'Content-Type: application/json' -d '{
  "jsonrpc":"2.0","id":1,"method":"tools/call",
  "params":{"name":"spec/plan","arguments":{}}}'
# result JSON is a string in .result.content[0].text
```

HTTP is the practical choice when many sub-agents need the engine
concurrently: sub-agents just need `curl`, no MCP client required.

Config that matters:

- `CREST_SPEC_SPEC_DIR` — where the CUE spec lives (run the server from the
  target project's root; generated files are written relative to it).
- `CREST_SPEC_GENERATE_MODEL` — a **label** recorded with commits and mixed
  into effective hashes. It does not make the engine call any model. Changing
  it re-plans everything, so keep it stable per project.
- `CREST_SPEC_EXECUTION_TIMEOUT` — startup recovery threshold for active
  executions abandoned by a crashed host (default `30m`; `0` disables).
  Recovery records an engine-observed host failure, not a model failure.
- One server per project directory. The SQLite db lives in `.crest-spec/`.

There is also a small human CLI (`crest-spec adopt`, `crest-spec dashboard`)
for one-off maintenance — that is the ONLY part of crest-spec meant to be
invoked as a command. Generation is never started from the CLI.

## Tool surface (what to call, when, and who calls it)

"Orchestrator" = your top-level loop. "Sub-agent" = a host-native agent you
spawned for one resource/check.

### Authoring & inspection (orchestrator / top-level agent)
| Tool | Purpose |
|---|---|
| `spec/validate` | Parse + registry-check the spec. Run after every spec edit. |
| `spec/plan` | Diff spec vs state → `create/modify/destroy` actions + waves. Review before running; a correct increment is small. |
| `spec/inspect` | One resource's full declaration (interface, invariants, prompts). |
| `spec/verification_definitions`, `spec/verifications`, `spec/verification_inspect` | List named validation/witness definitions and inspect exact commands, hashes, output, parsed observations, predicates, and evidence currency. |
| `spec/graph`, `spec/state`, `spec/status`, `spec/diff`, `spec/history`, `spec/log` | Read-only views of the graph/session/db. |
| `spec/sql` | Read-only SQL over the whole state db. The escape hatch for any question the views don't answer. |

### Session lifecycle (orchestrator)
| Tool | Purpose |
|---|---|
| `spec/begin` | Start a session: locks, snapshots the plan, seeds per-resource state. Returns `PendingDestroys` — destroys NEVER auto-execute. Options: `target`, `force` (re-serve one resource on purpose). |
| `spec/confirm_destroys` | Explicit human-gated deletion of the listed resources (files + state + checks). |
| `spec/next` | The work queue: returns the current wave's resources still needing generation. Call repeatedly; `done:true` ends the loop. |
| `spec/resolve` | Record triage: attach guidance to a failed resource and reset it for another attempt (UPDATE mode will carry the guidance). |
| `spec/skip` | Explicitly skip a resource — requires quoting the spec contradiction that justifies it. Not a convenience. |
| `spec/verify_wave`, `spec/wave_status` | Wave-level gate/status for whole-tree validation between waves. |
| `spec/finish` | Close the session. **Fail-closed**: named-validation/witness specs require the derived project state to be `complete`; legacy task checks must also be resolved. `force` exists but is a human decision and returns the actual incomplete status/reason/goals. |
| `spec/unlock` | Break a stale lock after a crash (human/orchestrator recovery). |

### Generation (called BY your generator sub-agents)
| Tool | Purpose |
|---|---|
| `spec/context` | Creates an immutable attempt and returns its `AttemptID`/`ContextManifestID`, budget allocation, ordered selection decisions, system prompt, and task prompt. Context is goal-directed: acceptance, task, resource contract, dependencies, consumers, relevant code, invariants, and retry evidence. In UPDATE mode it includes previous files, `## Previous Errors`, and `## Guidance`. Optional `role`, `budget_tokens`, and `parent_attempt_id` inputs shape the attempt without coupling crest-spec to a provider. |
| `spec/execution_start` | Required before agent work. Registers `crest-execution-v1`, the exact prepared role/context hash, host/provider/model, redacted settings, tools and permissions, template hashes, system instructions, and host session. Reusing an idempotency key with changed governed inputs fails closed. |
| `spec/commit` | Submit immutable candidate files by `attempt_id`, with optional duration/token/cost and host commit-reference metadata. Session, resource, model, plan operation, goals, and capabilities are derived from persisted state rather than trusted caller fields. The engine snapshots the candidate, runs declared validations, and atomically accepts or rejects it. |
| `spec/execution_report` | Finish an advisory role or explicitly record a cancelled, failed, or timed-out attempt that has no candidate. |
| `spec/note` | Attach a free-form note to the session/resource (shows up in later context). |

### Context inspection (orchestrator and humans)

| Tool | Purpose |
|---|---|
| `spec/context_attempts` | List attempts with resource, role, retry ancestry, budget, hash, and blocked state. |
| `spec/context_inspect` | Reconstruct the exact immutable system/task prompts and every included, truncated, or omitted source from SQLite. |
| `spec/context_compare` | Compare two manifests by source, decision, content hash, allocation, selector, budget, and template changes. |

Context creation and the resource's dispatch transition are one SQLite transaction. If mandatory goal, acceptance, task, system, and resource-contract material exceeds the bounded budget, the attempt is recorded as `context_blocked` and the resource remains pending. Runtime context manifests are never written into the project tree.

### Execution, role, and failure inspection

| Tool | Purpose |
|---|---|
| `spec/execution_roles` | Read the closed role registry, versioned context policy, expected output, permitted lifecycle actions, and handoffs. |
| `spec/executions`, `spec/execution_inspect` | List or reconstruct host/model/settings/tools, goals/capabilities, candidate, metrics, state events, goal progress, and disposition from SQLite. |
| `spec/failures` | List classified failures, evidence, deterministic corrective route, retry block, resolution, and affected goals. |
| `spec/failure_classify` | Append an evidence-backed classification. Host submissions require a `failure_triage` execution; human overrides cite `override_of` and preserve the original. |
| `spec/failure_resolve` | Record an explicit human resolution. An accepted retry automatically resolves unresolved failures in its ancestor chain. |
| `spec/handoffs` | Inspect pending or historical role handoffs and their source failure. |

The MCP `initialize` result advertises `capabilities.experimental.crestExecution`
with the execution protocol version, role-policy version, and legacy-commit
availability. Hosts should negotiate this before beginning a session. Secrets
must not be sent; crest-spec also recursively redacts credential-shaped keys
and bearer values before persistence.

### How this relates to DDD

Execution roles are responsibilities for an attempt, not new architectural
resource types. A `resource_implementer` may implement an aggregate, port,
adapter, or profiled asset; an `integration_implementer` may connect several
of those resources. The CUE DDD declaration remains the implementation
contract and dependency graph. Goals/capabilities supply project intent and
acceptance traceability. Context and execution manifests are runtime
governance records stored only in SQLite.

### Behavioral verification (triggered BY your orchestrator or verifier agents)
| Tool | Purpose |
|---|---|
| `spec/design_commit` | Commit a bounded context's observable contract (behaviors + typed predicates). |
| `spec/tasks_commit` | Commit per-resource tasks/checks derived from the contract. Structurally validated: prose or expression field names, missing bounds, etc. are rejected HERE, at authoring time. |
| `spec/verify` | Execute a named CUE witness by `witness_id`. crest-spec runs its real and negative commands against one source tree, parses both through one schema, applies the falsification gate, and stores the full provenance. Caller observations are rejected. An optional compatible `check_id` advances the legacy task ledger. |
| `spec/graduate` | Promote a passed legacy task check. Named witness evidence does not require graduation. |

### Amendments & learnings (optional, orchestrator)
`spec/amend`, `spec/list_amendments`, `spec/apply_amendments`,
`spec/graduate_amendment`, `spec/record_learnings`, `spec/learnings`,
`spec/promote_learnings` — a ledger for spec evolution and cross-session
lessons. `spec/promote_learnings` is now preview-only: a reusable change must
name a qualifying evaluation winner and pass the promotion flow below.
`spec/bootstrap`, `spec/import`, `spec/evolve`, `spec/mode`,
`spec/prompt`, `spec/vacuum` are setup/maintenance.

### Evaluations (host-driven, orchestrator and workers)

1. Capture complete historical attempts with `spec/evaluation_case_from_attempt`, or explicitly curate cases with `spec/evaluation_case_curate`.
2. Create a dataset, assign cases to training/development/held-out splits, and seal it. Sealing fixes its content identity.
3. Create immutable baseline/candidate configurations and a run.
4. Workers claim assignments with `spec/evaluation_assignment_claim`, then use the normal `spec/context` → `spec/execution_start` → candidate/report path. Heartbeat long work; release retryable work; cancel unsupported cases with a reason.
5. Submit the terminal ordinary `attempt_id` plus verifier-derived goal/integration/behavior/regression/intervention observations. crest-spec derives acceptance, retry, token, cost, duration, and candidate-churn facts from SQLite and overrides conflicting host values.
6. Finalize the run. Missing, unequal, or insufficient samples remain `inconclusive`; held-out outcomes are not exposed to workers.
7. Inspect conclusions and raw provenance with `spec/evaluation_runs` or the Evaluations dashboard. A reusable learning change then requires `spec/evaluation_learning_propose`, a human `spec/evaluation_promotion_decide`, and `spec/evaluation_promotion_apply` against the unchanged rollback identity.

Evaluation cases, leases, observations, comparisons, exact proposed changes,
and decisions are canonical SQLite state. Do not create evaluation manifests or
run-result sidecars in the project tree.

## The canonical orchestration loop

This is what YOUR workflow/script does (pseudocode; in Claude Code this is a
Workflow script spawning sonnet sub-agents; in another host, use its native
background-agent equivalent):

```
spec/validate                        # after any spec edit
plan = spec/plan                     # review: is the increment the size you expect?
begin = spec/begin                   # -> sessionId, PendingDestroys
if PendingDestroys: STOP, show the human, spec/confirm_destroys only on approval

loop:
  wave = spec/next(sessionId)
  if wave.done: break
  for resource in wave.resources (PARALLEL, one sub-agent each):
      sub-agent:
        ctx = spec/context(sessionId, resource, role) # immutable attempt + full context
        run = spec/execution_start(
          attempt_id=ctx.AttemptID,
          protocol_version="crest-execution-v1",
          role=ctx.Role,
          context_hash=ctx.ContextHash,
          host/model/settings/tools/templates=actual values)
        files = <your LLM writes/edits code from ctx>
        res = spec/commit(attempt_id=ctx.AttemptID, files=files, usage=actual values)
        if not res.Committed:
          read res.Validations and spec/failures
          request a child context with parent_attempt_id=ctx.AttemptID
          and the persisted handoff role, then retry (bounded)
  # wave gate: whole-tree validation
  if wave gate failed:
      triage (a sub-agent reads the errors, attributes them)
      spec/resolve(resource, guidance) for each attributed failure
      # next spec/next re-serves them in UPDATE mode with your guidance
  if stalled N times on the same resources: HALT LOUDLY. Never auto-skip.

spec/finish(sessionId)               # refuses until governed project completion is satisfied
git commit the spec + generated code together
```

Behavioral verification is a second loop: reconcile the named definitions,
then call `spec/verify(witness_id, session_id?)` for each required witness.
crest-spec—not the agent—runs and parses the real and negative cases. During
migration, design/task agents may still populate the legacy check ledger; pass
the compatible `check_id` to `spec/verify` and call `spec/graduate` afterward.

### Rules the orchestrator must respect

1. **One session/workflow at a time per project.** The state db and the
   target build tree are shared; concurrent sessions corrupt neither but
   contend on both (locks, cargo).
2. **Iterate, don't regenerate.** Retries get UPDATE-mode context with prior
   code + errors + guidance. A sub-agent that throws away the previous
   attempt and starts fresh is misbehaving.
3. **Halts are loud.** Non-convergence ends the run with an unresolved list
   for the human. `spec/skip` requires a quoted spec contradiction; `force`
   on `finish` is a human decision. There is no quiet path past any gate.
4. **Destroys are human-gated.** `begin` only reports them;
   `confirm_destroys` executes them.
5. **Sub-agents return data, not vibes.** The engine's tables are the truth:
   reconcile outcomes with `spec/sql`, never with a sub-agent's self-report.
6. **Report the actual execution identity.** `CREST_SPEC_GENERATE_MODEL`
   remains the plan/hash default, while the exact host/provider/model belongs
   in `spec/execution_start`; `spec/commit` cannot replace that identity.

## Porting to a non-Claude host (e.g. OpenCode)

You need three host-native capabilities; every serious agent CLI has them:

1. **An MCP client or plain HTTP** — connect the host to the server
   (stdio MCP for interactive use; HTTP for orchestrator scripts).
2. **Native sub-agent dispatch** — whatever the host calls "agents",
   "tasks", or "workers". Each generator/verifier sub-agent gets a prompt
   that says: call `spec/context` (or is handed the context inline), write
   the code, call `spec/commit`, handle rejection by iterating.
3. **A driver for the loop above** — a script or the top-level agent itself,
   holding the session id and pumping `next → dispatch → gate → resolve`.

What you must NOT build: any bridge that "sends the task to crest-spec" and
expects orchestration back. The engine will simply answer state queries and
gate commits — if nobody on the host side spawns generators, no code is ever
written.

Host adapters live under `docs/hosts/` and must preserve this contract:

- `docs/hosts/claude-code.md` — the current reference implementation.
- `docs/hosts/opencode.md` — OpenCode MCP, subagent, command, and permission templates.

Reference implementations (Claude Code Workflow scripts, directly portable
as pseudocode): `.claude/workflows/spec-generate.js` in this repo and the
HTTP-transport drivers in `crest-synth/.crest-spec/*-http.js`
(`spec-generate-http.js`, `spec-behavioral-http.js`, `spec-reauthor-http.js`,
`spec-verify-pending-http.js`).

A host adapter may add config, agents, commands, skills, or scripts for that
host. It must not add a crest-spec generation CLI, LLM client, workflow runtime,
or sub-agent dispatcher.

## Anti-patterns (all observed live, all fatal)

- **"crest-spec will interpret the task and dispatch its sub-agents."** It
  has neither. See the first section.
- **Calling the binary as a job CLI** (`crest-spec generate`, `crest-spec
  run`). No such subcommands; generation only happens through session tools
  driven by your agents. The CLI surface is `adopt` and `dashboard` only.
- **Two orchestrators against one project** (two sessions, or a session
  while another host is mid-wave). Locks will fight; cargo will fight.
- **Self-reported success.** A sub-agent claiming "verified" without a
  SQLite-backed validation run and current evidence did nothing. Inspect it
  with `spec/verification_inspect`.
- **Blank-slate retries.** If your sub-agent ignores the served previous
  attempt and errors, you've re-created the expensive failure mode UPDATE
  mode exists to prevent.
- **Editing the spec mid-session.** Plan and hashes are snapshotted at
  `begin`; edit specs between sessions, validate, re-plan.
