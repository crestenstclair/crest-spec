# Claude Code adapter for crest-spec

Claude Code is the first host adapter for crest-spec and currently acts as the reference implementation for the host-side orchestration loop.

## Control model

Claude Code is the agent host and orchestrator. crest-spec is a passive MCP state engine. Claude Code workflows and sub-agents do all thinking, generation, verification, retry policy, and human escalation.

## MCP configuration

Register crest-spec as a local stdio MCP server in the target project's `.mcp.json`:

```json
{
  "mcpServers": {
    "crest-spec": {
      "command": "/absolute/path/to/crest-spec",
      "env": {
        "CREST_SPEC_SPEC_DIR": "./spec",
        "CREST_SPEC_GENERATE_MODEL": "claude-sonnet-5"
      }
    }
  }
}
```

The model value is a stable state label. Claude Code chooses the actual model for its workflow agents.

## Reference workflow

The canonical Claude Code workflow is:

- `.claude/workflows/spec-generate.js`

It implements:

1. design contract agents per bounded context;
2. task/check agents per resource;
3. one bounded reusable generator pool refilled by capacity-limited `spec/context_batch` calls;
4. independent verifier sub-agents per behavioral check;
5. wave verification, triage, resolve, and stall handling;
6. finish-time blocker handling and learning capture.

Claude Dynamic Workflows do not currently expose a follow-up/resume handle for
an `agent()` after it returns. The adapter therefore makes each pool slot one
autonomous agent for an entire generation pass: that agent pulls, claims, and
processes multiple sequential resources before returning. It does not launch a
fresh agent per resource under a reused logical ID. The bounded pool is
recreated only after it drains and the verification pass completes.

The companion skill is:

- `.claude/skills/spec-generate/SKILL.md`

Use this adapter as executable pseudocode for other hosts. Do not move its responsibilities into crest-spec.

## HTTP drivers

For hosts or scripts that need fan-out without a native MCP client per worker, use crest-spec's HTTP transport:

```bash
CREST_SPEC_SPEC_DIR=./spec \
CREST_SPEC_HTTP_ADDR=127.0.0.1:8792 \
CREST_SPEC_GENERATE_MODEL=claude-sonnet-5 \
/absolute/path/to/crest-spec
```

Reference HTTP-transport drivers live outside this repository in crest-synth under `.crest-spec/*-http.js`:

- `spec-generate-http.js`
- `spec-behavioral-http.js`
- `spec-reauthor-http.js`
- `spec-verify-pending-http.js`

## Adapter invariants

- Claude Code workflows spawn the sub-agents; crest-spec never does.
- Each autonomous worker slot calls `spec/context_batch(limit=1)` when it is
  idle. Across the bounded pool this equals immediate worker capacity and does
  not prefetch a whole wave.
  Each returned context is immutable but unclaimed; repeated calls replay any
  unclaimed context and refill newly idle capacity. Blocked contexts are
  reported without dispatch or assignment.
- For each unblocked prepared context, the autonomous slot records
  `dispatch_requested` with its `DispatchID` and stable, nonempty host and
  worker identities before inspecting or following the resource prompt. It
  then records `worker_acknowledged` with the same
  identities before `spec/execution_start`. Both events are mandatory, and
  CrestSpec rejects overlapping nonterminal assignments to the same worker.
  If initial dispatch returns `rebuild_context`, it does not start the stale
  abandoned attempt and refills through `spec/context_batch`. A live slot that
  cannot reach execution after claiming records `dispatch_failed` with the
  same identities and a concrete reason before refilling. Agent launch failure
  occurs before this self-pulling worker has claimed anything; a hard process
  death relies on persisted orphan recovery because dead code cannot report an
  event. The worker calls `spec/commit` by attempt ID directly.
- A rejected assignment fetches an UPDATE-mode context through single-resource
  `spec/context`, then records `dispatch_requested` for that new `DispatchID`
  and `worker_acknowledged` before its next execution. A `rebuild_context`
  response abandons a stale snapshot and requires a fresh context without
  consuming a generation retry.
- Retries must use UPDATE-mode context.
- Verification must execute a declared witness through `spec/verify`; inspect the resulting SQLite-backed run and evidence rather than accepting agent-supplied observations.
- Operational diagnosis should start with `spec/project_overview`, then follow `spec/plan_inspect`/`spec/impact` into `spec/attempt_inspect`, `spec/context_inspect`, `spec/execution_inspect`, and `spec/verification_inspect` instead of reconstructing provenance from project files.
- `spec/skip` and `spec/finish force` are human-level decisions.
