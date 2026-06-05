# Agent CLI: Session-Based Sub-Agent Orchestration

## Problem

crest-spec's `apply` command runs as a batch: plan all resources, invoke the LLM for each, validate, commit. There are no interactive checkpoints. A human cannot pause, inspect, redirect, or intervene mid-apply. Sub-agents (e.g. Claude Code agents) cannot be used as the implementation engine because the constraint loop is tightly coupled to `ILlmClient.generate()`.

## Goal

Add a session-based CLI mode (`crest-spec agent`) that lets an external orchestrator drive the apply process one resource at a time. The orchestrator (a Claude Code skill, workflow, or manual session) controls pacing, spawns sub-agents with scoped prompts, and can pause or redirect between resources.

## Design

### CLI Commands

Six subcommands under `crest-spec agent`. All output JSON to stdout. Human-readable logs go to stderr.

| Command | Purpose |
|---------|---------|
| `agent begin` | Start session: load spec, acquire lock, compute plan + waves, create apply record, persist session to DB. |
| `agent next` | Return next available resource(s) in current wave. Advances waves automatically. |
| `agent context <id>` | Return the scoped prompt (system + resource + dependency files + module tree) for a resource. |
| `agent validate <id>` | Run validation checks (invariants, type check, tests) against files currently on disk. |
| `agent commit <id>` | Accept a validated resource: update state DB, record generation audit trail. |
| `agent finish` | Finalize apply record, release lock, clean up session. |

### Output Schemas

**`agent begin`**
```json
{
  "applyId": 42,
  "plan": [
    {"resourceId": "vo.Kernel.MidiGroup", "action": "create", "reason": "new resource"},
    {"resourceId": "agg.Audio.SineVoice", "action": "modify", "reason": "declaration changed"}
  ],
  "waves": [
    ["vo.Kernel.MidiGroup", "vo.Kernel.NoteNumber"],
    ["agg.Audio.SineVoice"]
  ],
  "totalResources": 3
}
```

**`agent next`**
```json
{
  "wave": 0,
  "resources": [
    {"resourceId": "vo.Kernel.MidiGroup", "action": "create", "reason": "new resource"}
  ],
  "done": false
}
```

When all resources are committed: `{"wave": -1, "resources": [], "done": true}`

**`agent context <id>`**
```json
{
  "resourceId": "vo.Kernel.MidiGroup",
  "systemPrompt": "You are a rust code generator...",
  "prompt": "## Resource: valueObject \"MidiGroup\" (vo.Kernel.MidiGroup)\n..."
}
```

**`agent validate <id>`**
```json
{
  "resourceId": "vo.Kernel.MidiGroup",
  "passed": false,
  "errors": [
    "Type check failed:\nsrc/Kernel/MidiGroup/mod.rs:12 - expected u8, found i32",
    "Invariant violated: must be 0-15 - no range check in constructor"
  ]
}
```

**`agent commit <id>`**

Discovers files on disk using the same convention as `validate` (`src/{Context}/{Resource}/` and `tests/{Context}/{Resource}/`). Records all discovered files in `generated_files`.

```json
{
  "resourceId": "vo.Kernel.MidiGroup",
  "committed": true,
  "filesRecorded": ["src/Kernel/MidiGroup/mod.rs", "tests/Kernel/MidiGroup/mod.rs"]
}
```

**`agent finish`**
```json
{
  "applyId": 42,
  "status": "ok",
  "created": 2,
  "modified": 1,
  "destroyed": 0,
  "failed": 0
}
```

### Session State: SQLite

All session state lives in the database. No side-channel files.

**New table:**
```sql
CREATE TABLE IF NOT EXISTS agent_sessions (
  apply_id INTEGER PRIMARY KEY REFERENCES applies(id),
  plan_json TEXT NOT NULL,
  waves_json TEXT NOT NULL,
  hashes_json TEXT NOT NULL,
  created_at TEXT NOT NULL
);
```

- `plan_json`: serialized `PlannedAction[]` snapshot from `begin`
- `waves_json`: serialized `string[][]` (resource IDs per wave, computed by WaveComputer)
- `hashes_json`: serialized `Record<string, string>` (effective hashes for all resources)

**Session identification:** The active session is the `applies` row with `status = 'running'`. The lock ensures only one exists.

**Wave progression:** `agent next` reads the session's `waves_json`, checks `apply_actions` for the running apply to see which resources have `outcome = 'success'`, and returns resources from the earliest wave that has uncommitted resources.

### Orchestrator Flow

```
1. orchestrator runs: crest-spec agent begin
   -> gets plan with waves

2. orchestrator runs: crest-spec agent next
   -> gets next resource(s) available

3. for each resource:
   a. orchestrator runs: crest-spec agent context <id>
      -> gets scoped prompt

   b. orchestrator spawns sub-agent with prompt as guidance
      -> sub-agent writes files to disk

   c. orchestrator runs: crest-spec agent validate <id>
      -> if errors: feed errors to sub-agent, sub-agent fixes, goto (c)
      -> if passed: continue

   d. orchestrator runs: crest-spec agent commit <id>
      -> resource recorded in state DB

4. orchestrator runs: crest-spec agent next
   -> if done: false, goto 3
   -> if done: true, continue

5. orchestrator runs: crest-spec agent finish
   -> session finalized
```

Between any of these steps, the human can intervene: inspect files, modify the sub-agent's output, skip a resource, or abort entirely.

### Validation Details

`agent validate <id>` runs three check layers in order:

1. **File discovery**: Scan disk for files matching the resource's expected paths (convention: `src/{Context}/{Resource}/`). Also check files recorded in `generated_files` for this resource.
2. **Invariant check**: Run `InvariantChecker.checkGenerated()` against discovered files.
3. **Type check**: Run the project's `typeCheckCommand` (if configured in meta).
4. **Tests**: Run the project's `testCommand` (if configured in meta).

`validate` does NOT write files. It only reads and checks. The sub-agent (or human) is responsible for writing files to disk.

`validate` does NOT invoke the LLM for verification. The LLM verify step from the batch constraint loop is omitted in agent mode -- the sub-agent IS the LLM, so self-verification is redundant.

### What Changes vs What Stays

**New files:**
- `src/cli/commands/agent.ts` -- the six subcommands
- `src/engine/agent-session.ts` -- session read/write, wave progression logic
- `src/engine/resource-validator.ts` -- extracted validation (invariants + type check + tests, no LLM)
- `src/state/schema.ts` -- add `agent_sessions` table DDL + bump schema version
- `tests/engine/agent-session.test.ts` -- session lifecycle tests
- `tests/engine/resource-validator.test.ts` -- validation tests
- `tests/cli/agent-commands.test.ts` -- integration tests for CLI commands

**Modified files:**
- `src/cli/main.ts` -- add `agent` command routing
- `src/state/state-database.ts` -- add session CRUD methods

**Untouched:**
- `ApplyEngine` -- batch mode stays as-is
- `ConstraintLoop` -- stays as-is for batch mode
- `LlmClient` -- not used in agent mode
- `PromptBuilder` -- reused as-is
- `Planner`, `HashComputer`, `WaveComputer` -- reused as-is
- `InvariantChecker` -- reused as-is
- DSL / spec format -- no changes

### CLI Flags

All `agent` subcommands accept:
- `--spec <file>` -- spec file (default: `crest-spec.ts`)
- `--model <id>` -- model ID for hash computation (default: `claude-sonnet-4-6`)

`agent begin` additionally accepts:
- `--target <resource>` -- scope session to a specific resource and its cascades
- `--force` -- force re-render of all resources

`agent validate` additionally accepts:
- `--skip-typecheck` -- skip type check step
- `--skip-tests` -- skip test step

### Error Handling

- **`begin` when session already active**: Error with existing session details. User must `finish` or `unlock` first.
- **`next`/`context`/`validate`/`commit` without active session**: Error directing user to run `begin` first.
- **`commit` without prior `validate` pass**: Allowed. The orchestrator is trusted. Validation is advisory.
- **`commit` for resource not in plan**: Error. Only planned resources can be committed.
- **`finish` with uncommitted resources**: Allowed. Uncommitted resources are recorded as skipped. Apply status reflects whether all planned resources succeeded.
- **Process crash mid-session**: Lock remains. `unlock` clears it. The apply record stays as `running` and can be inspected via `log`.
