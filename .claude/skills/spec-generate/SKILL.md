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
- One generator agent per resource calls `spec_context → generate files → spec_commit`
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
- `malformed` → **QUARANTINE**, never regenerate. `spec/verify` sets this state
  when a check's predicate is structurally invalid — a type mismatch (e.g. `eq`
  against a string/bool field) or an unknown op — as opposed to `failed` (the
  behavior simply isn't exhibited) or `theater` (non-discriminating). A
  structurally broken check is the **check's fault, not the implementation's**:
  no amount of regenerated code can ever satisfy a predicate that can't
  type-check against the field it targets. The workflow does not call
  `spec_resolve` for these — it never blocks the resource or counts as a
  generation failure. Instead each malformed check is collected into a
  `quarantined` list (`{resource_id, check_id, reason}`) for a human /
  spec-author to fix the check definition itself.
- `failed` / `theater` → failure → `spec_resolve` (regenerate)
- `pending` / absent → failure → `spec_resolve` (regenerate). A check still
  pending after its verifier ran means the verifier crashed, timed out, returned
  null, skipped `spec/verify`, or `spec/verify` rejected a missing/malformed
  witness — the behavior is unverified, so the resource regenerates. A
  crashed/empty verifier is never silently dropped.

Verifier agents run in parallel per check (within a resource). The stall guard
bounds infinite cycles if behavioral checks never converge.

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
5. Review triaged list; surface skips to user.
6. spec_finish     — if reflection_prompt non-empty, run with sonnet → spec_record_learnings.
7. Review graduated list — each entry is a confirmed behavioral check; use
   spec-authoring skill to author them back into the spec invariants.
8. Review quarantined list — each entry is a check with a structurally invalid
   predicate; it was never regenerated against, and needs a human/spec-author
   fix to the check definition, not the resource code.
```

Use `args.model` to override the generation model. Never haiku.

## Return value

```json
{
  "waves_processed": N,
  "triaged": [...],
  "graduated": [{"resource_id": "...", "check_id": "...", "state": "graduated"}],
  "quarantined": [{"resource_id": "...", "check_id": "...", "reason": "..."}],
  "behavioral_checks_total": N,
  "next_steps": "..."
}
```

## Failure handling

- Generator retries are bounded by maxRetries (default 3) inside the generator agent.
- Behavioral check failures feed back via `spec_resolve` (same mechanism as
  compile failures) — the resource cycles into the next wave pass with failure context.
- Malformed checks (structurally invalid predicate) are QUARANTINED, not fed
  back via `spec_resolve` — they cannot converge through regeneration, so
  looping them would stall the wave forever. They're reported for a human /
  spec-author to fix instead.
- The stall guard (MAX_STALLS=2, MAX_STALLS_HARD=5) terminates infinite cycles
  by force-skipping culprit resources.
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

- Never write generated-resource code in the main session — every file comes
  from a sub-agent via `spec_commit`.
- Waves are sequential; resources within a wave run in parallel.
- Design and tasks phases run once per session (before the wave loop).
- Behavioral checks live only in sqlite (never in .cue files) until graduated
  and manually authored into invariants by the spec-authoring skill.
- Validation failures are signal, not noise: read them before retrying scope.
