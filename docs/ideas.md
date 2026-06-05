# Ideas

## Sub-agent states

Right now `agent next` treats resources as binary: committed or not. There's no
visibility into what a sub-agent is actually doing, or why it stopped. Add a
state machine per resource tracked in SQLite so the orchestrator loop can react.

### States

```
pending ──> dispatched ──> completed ──> committed
                │                │
                ├──> blocked     │
                ├──> errored     └──> rejected
                └──> timed_out
```

| State        | Meaning                                                        |
|--------------|----------------------------------------------------------------|
| `pending`    | In the plan, not yet dispatched to a sub-agent                 |
| `dispatched` | Sub-agent has been spawned and is working                      |
| `completed`  | Sub-agent returned output; files written but not yet committed |
| `committed`  | Files validated and recorded in state DB                       |
| `blocked`    | Sub-agent needs a decision from the user or another resource   |
| `errored`    | Sub-agent failed (compile error, invariant violation, crash)   |
| `timed_out`  | Sub-agent didn't respond within deadline                       |
| `rejected`   | Validation failed after completion; needs re-dispatch          |

### Blocked context

When a sub-agent reports `blocked`, it should store structured context:

```ts
interface BlockedContext {
  resourceId: string;
  reason: string;           // human-readable summary
  blockedOn?: string;       // another resourceId, if dependency-blocked
  question?: string;        // question for the user, if decision-blocked
  retryable: boolean;       // can the orchestrator retry automatically?
}
```

### Errored context

```ts
interface ErrorContext {
  resourceId: string;
  errorKind: "compile" | "invariant" | "runtime" | "parse" | "unknown";
  message: string;
  files?: string[];         // which generated files were involved
  retryCount: number;       // how many times we've retried
  maxRetries: number;       // give up after this many
}
```

## Post-wave orchestrator loop

After dispatching all sub-agents in a wave, the orchestrator enters a
check-loop instead of assuming everything succeeded:

```
dispatch wave N
  │
  ├── poll states from SQLite ──────────────────────────┐
  │                                                     │
  │   all completed/committed?  ──> advance to wave N+1 │
  │   any blocked?              ──> report to user      │
  │   any errored (retryable)?  ──> re-dispatch         │
  │   any errored (exhausted)?  ──> report to user      │
  │   any timed_out?            ──> report to user      │
  │   still dispatched?         ──> wait + re-poll      │
  │                                                     │
  └─────────────────────────────────────────────────────┘
```

The key change: the orchestrator **reports back to the user** for any
non-happy-path state instead of silently skipping or failing at `finish`.
The user gets a summary like:

```
Wave 2: 5 resources
  completed: VoiceAllocator, AudioRenderer, SynthEngine
  blocked:   CpalAudioOutput — "need user decision: try_send vs send for backpressure"
  errored:   FilterState — compile error (attempt 2/3, retrying)
```

The user can then:
- Answer the question (unblocks the agent)
- Force-retry an errored resource
- Skip a resource and continue
- Abort the session

## Validation object

The current `ResourceValidator` runs generic type-check and test commands.
Add a way to declare resource-specific validations in the spec itself — 
integration tests, output checks, behavioral assertions.

### Declaration in spec

```ts
app.asset("CpalAudioOutputAdapter", {
  kind: "rust-adapter",
  description: "...",
  prompts: [...],
  validations: [
    {
      kind: "compiles",
      command: ["cargo", "build"],
    },
    {
      kind: "test",
      command: ["cargo", "test", "--lib", "cpal_audio_output"],
      description: "unit tests for the cpal adapter pass",
    },
    {
      kind: "integration",
      command: ["cargo", "run", "--", "--wav"],
      assertions: [
        { kind: "exit_code", expected: 0 },
        { kind: "file_exists", path: "tone-test.wav" },
        { kind: "stdout_contains", pattern: "Wrote tone-test.wav" },
      ],
      description: "arpeggio renders to WAV without errors",
    },
    {
      kind: "custom",
      command: ["python3", "scripts/check-wav-frequencies.py", "tone-test.wav"],
      description: "WAV contains all 3 expected frequencies at correct time offsets",
    },
  ],
});
```

### Validation kinds

| Kind          | What it checks                                      |
|---------------|-----------------------------------------------------|
| `compiles`    | Build command exits 0                               |
| `test`        | Test command exits 0                                |
| `integration` | Run command + structured assertions on output       |
| `custom`      | Arbitrary script — exit 0 = pass, nonzero = fail    |

### Assertions (for `integration` kind)

| Assertion         | Fields                    |
|-------------------|---------------------------|
| `exit_code`       | `expected: number`        |
| `file_exists`     | `path: string`            |
| `file_not_empty`  | `path: string`            |
| `stdout_contains` | `pattern: string`         |
| `stderr_empty`    | (none)                    |
| `file_matches`    | `path, regex: string`     |

### How it fits

The `validateAsync` path on `ResourceValidator` would check for these
declarations on the resource and run them after the generic checks. Failures
feed back into the sub-agent state machine — `completed` → `rejected` with
the validation output attached, triggering a re-dispatch.
