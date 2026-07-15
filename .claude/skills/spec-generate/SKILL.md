---
name: spec-generate
description: Use when the user asks to run/apply/generate a crest-spec session ("run the spec", "generate phase N", "apply the spec") — drives the full native generation pipeline via the spec-generate workflow with sonnet sub-agents
---

# crest-spec native generation

You are the orchestrator. The crest-spec MCP server is a pure state engine —
it never runs LLMs. Generation happens in YOUR sub-agents via the
spec-generate workflow. Default model: sonnet. Never haiku.

## Pipeline

The workflow runs four ordered stages per session:

### Stage 1 — Design (pre-loop, once)
One discovery agent groups all resources into bounded contexts (by naming
convention + dependency graph). Then one design agent per context derives an
**observable contract**: the public interface surface plus behavioral claims
expressed as measurable predicates. Contracts are committed via `spec/design_commit`
and stored in the `designs` table (keyed by spec_hash).

Design agents run in parallel across contexts.

### Stage 2 — Tasks (pre-loop, once)
One tasks agent per resource reads the context contract and decomposes it into
resource-level tasks, each carrying one or more **behavioral checks**. A check
is `{ behavior: string, predicates: [{ field, op, value/min/max/member }] }`
using the predicate vocabulary (eq, gt, gte, lt, lte, range, count,
distinct_count, differ, monotonic, set_contains, equals). Tasks are committed via
`spec/tasks_commit` into the `tasks` and `checks` tables.

**Predicate typing rule** — each predicate's `field`/`op` MUST match the JSON type
the witness will emit: numeric ops (eq, gt, gte, lt, lte, range) require a NUMBER
field; `equals` is used for BOOLEAN, STRING, and ENUM fields (set `member` to the
expected value — never use `eq` on a boolean or string); list ops (count,
distinct_count, differ, set_contains) require a JSON ARRAY field.

Tasks agents run in parallel across resources.

### Stage 3 — Generate (wave loop)
The existing generation loop:
- `spec_next` returns the next wave of resources (dependency order)
- One role-specific agent per resource calls
  `spec_context → spec_execution_start → generate files → spec_commit(attempt_id)`
  with internal retry on rejection (up to maxRetries)
- Generator agents within a wave run in parallel

### Stage 4 — Verify (per check, after each wave's commits)
After each wave's generators commit, for every committed resource the workflow
lists its pending behavioral checks and runs a **separate, independent verifier
agent** per check (not the generator). The verifier:

1. Reads only the spec declaration (`spec_inspect`) — never the committed files
2. Authors a **real witness harness** that calls the public interface and emits
   `CREST_OBS:{...}` (measured field values) on stdout
3. Authors a **stub baseline** (degenerate no-op implementation) that emits
   `CREST_STUB:{...}` with the same measurements
4. Runs both, parses the JSON lines, calls `spec/verify` with
   `{check_id, real_observation, stub_observation}`
5. `spec/verify` is **fail-closed**: passes only when real passes AND stub fails
   (theater = stub also passes → rejected as non-discriminating). It is also
   strict on the stub side — an empty or field-missing `stub_observation`, or a
   missing/malformed `CREST_OBS`/`CREST_STUB` witness line, is rejected as a
   "malformed verification" (passed:false), never a skip. Both CREST_OBS and
   CREST_STUB must emit every predicate field with the correct JSON type (NUMBER
   for numeric ops; JSON ARRAY for list ops; bool/string/number for `equals`).
6. On pass: calls `spec/graduate` — state transitions to `graduated`
7. CLEANUP: after capturing the two JSON lines the verifier deletes its witness
   harness files and any compiled `crest_witness_*` binaries from /tmp so they
   do not pollute the generated crate. Witness source is always written to /tmp
   (never inside the project's src/ directory).

**The workflow does NOT trust the verifier agent's self-report.** After the
verifier agents run, the workflow reads the **authoritative engine state** from
the `checks` table (`spec_sql`) and drives pass/fail off that:

- `passed` / `graduated` → success
- `malformed` → **RE-AUTHOR the check, then block**. `spec/verify` sets this
  state when a check's predicate is structurally invalid — a type mismatch
  (e.g. `eq` against a string/bool field) or an unknown op. A structurally
  broken check is the **check's fault, not the implementation's**: regenerated
  code can never satisfy a predicate that can't type-check. The workflow
  therefore re-runs the TASKS stage for that resource once (with the
  malformation reasons as feedback; `spec/tasks_commit` replaces the prior
  check set) and re-verifies. A check still malformed after re-authoring goes
  on the `blocked` list — and `spec/finish` REFUSES to finalize the session
  while any such check exists. There is no quarantine and no quiet exit from
  enforcement.
- `failed` / `theater` → failure → `spec_resolve` (regenerate)
- `pending` / absent → failure → `spec_resolve` (regenerate). A check still
  pending after its verifier ran means the verifier crashed, timed out, returned
  null, skipped `spec/verify`, or `spec/verify` rejected a missing/malformed
  witness — the behavior is unverified, so the resource regenerates. A
  crashed/empty verifier is never silently dropped.

Verifier agents run in parallel per check (within a resource). If a wave
repeats past its pass budget the run HALTS loudly with the unresolved
resources listed — nothing is auto-skipped.

### Post-wave: project-level verification
After behavioral verify, `spec_verify_wave` runs compile/test/fmt checks.
Failures are attributed to owning resources via `spec_sql` and fed back via
`spec_resolve`. Both feedback types (behavioral + project) inject before the
next `spec_next` call.

## Invocation

```
1. spec_plan       — review what will change. If empty, stop.
2. spec_begin      — returns session_id, plan, waves, PendingDestroys.
3. spec_confirm_destroys — if PendingDestroys is non-empty, confirm first.
4. Workflow({scriptPath: ".claude/workflows/spec-generate.js", args: {sessionId: "<session_id>"}})
5. Review triaged + unresolved lists; surface halts and skips to the user.
6. spec_finish     — REFUSES (Blocked=true, blocking_checks listed) while any
   behavioral check is pending/failed/theater/malformed. Resolve the blockers;
   force=true is an explicit human decision, never the default. If
   reflection_prompt non-empty, run with sonnet → spec_record_learnings.
7. Review graduated list — each entry is a confirmed behavioral check; use
   spec-authoring skill to author them back into the spec invariants.
8. Review blocked list — checks still structurally malformed after one
   re-authoring pass; fix the check definitions (tasks stage), not the code.
```

Use `args.model` to override the generation model. Never haiku.

## Return value

```json
{
  "waves_processed": N,
  "triaged": [...],
  "graduated": [{"resource_id": "...", "check_id": "...", "state": "graduated"}],
  "blocked": [{"resource_id": "...", "check_id": "...", "reason": "..."}],
  "unresolved": [{"resource_id": "...", "attempts": N, "last_error": "..."}],
  "behavioral_checks_total": N,
  "next_steps": "..."
}
```

## Failure handling

- Generator retries are bounded by maxRetries (default 3) inside the generator
  agent. Every retry is a new immutable attempt with its own SQLite execution
  manifest. `spec_context` selects the required role (including a routed role
  handoff), injects the prior failure (`## Previous Errors`), any
  triage guidance (`## Guidance`), AND the previous attempt's files (UPDATE
  mode) into every retry prompt — retries iterate on existing code with a
  minimal fix; they never start over because of a minor bug.
- Behavioral check failures feed back via `spec_resolve` (same mechanism as
  compile failures) — the resource cycles into the next wave pass with failure context.
- Malformed checks (structurally invalid predicate) are RE-AUTHORED once via
  the tasks stage; if still malformed they land on `blocked` and `spec_finish`
  refuses until they are fixed. They are never silently dropped.
- If a wave repeats past its pass budget (MAX_STALLS=3) the run HALTS loudly
  and returns the unresolved resources. Nothing is auto-skipped: decide
  explicitly — better `spec_resolve` guidance, a spec fix, or a `spec_skip`
  whose reason quotes the actual spec contradiction.
- If the whole workflow dies: `spec_status`/`spec_wave_status` show where it
  stopped; re-invoking with the same sessionId resumes (spec_next re-serves
  non-terminal resources; design/tasks are idempotent via upsert).
- Stale lock: `spec_unlock`, then `spec_begin` again.

## Anti-theater guarantee

The verifier sub-agent is dispatched independently of the generator — it never
sees the committed implementation files. The stub baseline must be a genuine
no-op so that a trivially-passing check (theater) is caught and rejected.
`spec/verify` enforces this server-side: real-pass + stub-fail = pass;
any other combination (including empty/malformed stub) = fail. The orchestrator
closes the last loophole by reconciling against engine check state rather than
the verifier's self-report, so a verifier that crashes or lies about passing
without recording a real verification leaves its check `pending` and is caught
as a failure.

## Rules

- Never write generated-resource code ANYWHERE — not in the main session, not
  via a sub-agent's own file tools. Every file enters the project through
  `spec_commit` so the loop's feedback stays coherent. (The user's own hand
  edits to generated files are fine — once generated, the disk is theirs;
  the planner never polices content.)
- Every generator must call `spec_execution_start` with
  `crest-execution-v1`, the exact context/template hashes, role, host, model,
  settings, tools, permissions, and system instructions before generating.
  `spec_commit` accepts `attempt_id`, files, and notes only; it derives all
  other authority from canonical SQLite state.
- `spec_skip` is for a WRONG SPEC (quote the contradiction), never for hard
  work or non-convergence. Halts are resolved by humans, not by skipping.
- Waves are sequential; resources within a wave run in parallel.
- Design and tasks phases run once per session (before the wave loop).
- Behavioral checks live only in sqlite (never in .cue files) until graduated
  and manually authored into invariants by the spec-authoring skill.
- Validation failures are signal, not noise: read them before retrying scope.
