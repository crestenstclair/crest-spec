# Wave Dispatch Observability Gap

## Problem

When `spec/run_wave` dispatches resources in parallel, the orchestrator (and user) has no way to distinguish between resources that are:
1. Queued behind the concurrency semaphore (waiting for a slot)
2. Actively generating code (claude subprocess running)
3. In review/validation (post-generation constraint checking)

All three states report identically: `state: "dispatched", attempts: 0`.

This makes it impossible to diagnose stalls, estimate completion time, or understand throughput bottlenecks without shelling out to `ps aux | grep claude`.

## Root Cause

Three design gaps compound into the problem:

### 1. Single "dispatched" state covers the entire generation lifecycle

`Context()` (session.go) transitions the resource to `StateDispatched` immediately — before `eng.Generate()` acquires the concurrency semaphore. A resource blocked on the semaphore for 2 minutes looks identical to one mid-generation.

```
Current:   pending → dispatched → committed/rejected/errored
                     ^^^^^^^^^^
                     This one state covers: queued, generating, reviewing, validating
```

### 2. Attempts counter only updates on commit

`session_resources.attempts` stays at 0 throughout the entire constraint loop. It only increments inside `Commit()` (session.go ~line 380). A resource on its 3rd retry attempt still shows `attempts: 0` in `wave_status` until it either commits or is rejected.

### 3. No timing information exposed

`wave_status` returns state and attempts but no timestamps. There's no `dispatched_at` or elapsed duration, so the observer can't tell if a resource has been running for 5 seconds or 5 minutes.

## Impact

- **Diagnosis requires shell access**: The only way to check if generation is progressing is `ps aux | grep claude` to count sub-agent processes.
- **Stalls are invisible**: If a resource hangs (subprocess deadlock, API timeout), it looks the same as a resource that just started.
- **Semaphore contention is hidden**: With `MaxConcurrency=5` and 13 resources, 8 queue invisibly. The user sees no indication of throughput limits.
- **Resource count mismatch is confusing**: `spec/status` reports 22 total resources in the wave, but `wave_status` only returns 13 (the generatable ones). The 9 infrastructure resources (contexts, asset kinds, project scaffolding) are counted but not visible.

## Proposed Fixes

### Fix 1: Add sub-states to the dispatch lifecycle

Add granular states or a `phase` field to `session_resources`:

```
pending → queued → generating → reviewing → validating → committed
                                                       → rejected
                                                       → errored
```

Update points:
- `queued`: Set in `dispatchWaveResources()` before the goroutine calls `dispatchResource()`
- `generating`: Set after `eng.Generate()` acquires the semaphore (requires a callback from `Engine.acquire()`)
- `reviewing`: Set before `runReviewStep()` in `runAttempt()`
- `validating`: Set before `runValidationStep()` in `runAttempt()`

A lighter alternative: add a `phase` string field to `session_resources` and update it at each transition without changing the core state machine.

### Fix 2: Update attempts counter incrementally

In `runConstraintLoop()`, update `session_resources.attempts` at the START of each attempt, not at commit:

```go
// loop.go, inside the attempt loop
for attempt := 1; attempt <= maxAttempts; attempt++ {
    opts.Store.UpdateSessionResourceState(
        sessionID, resourceID, string(StateDispatched),
        "", "", attempt, "",
    )
    rec := runAttempt(ctx, eng, opts, attempt, &lastOutput, &lastError)
    // ...
}
```

This requires threading `sessionID` and `resourceID` into `LoopOpts` (they're already there as `ApplyID` and `ResourceID`).

### Fix 3: Add timing to wave_status

Record `dispatched_at` when `Context()` transitions the resource, and expose elapsed time in the `WaveStatus` response:

```go
type ResourceDetail struct {
    ResourceID   string  `json:"resource_id"`
    State        string  `json:"state"`
    Phase        string  `json:"phase,omitempty"`     // new
    Attempts     int     `json:"attempts"`
    MaxRetries   int     `json:"max_retries"`
    LastError    string  `json:"last_error,omitempty"`
    ElapsedMS    int64   `json:"elapsed_ms,omitempty"` // new
    DispatchedAt string  `json:"dispatched_at,omitempty"` // new
}
```

### Fix 4: Surface semaphore utilization

Expose current/max concurrency in `spec/status`:

```go
type SessionStatusResult struct {
    // existing fields...
    Concurrency struct {
        Active int `json:"active"`
        Max    int `json:"max"`
        Queued int `json:"queued"`
    } `json:"concurrency"`
}
```

This requires the `Engine` to expose its semaphore state — e.g., `Engine.ActiveCount()` returning `len(sem)`.

### Fix 5: Emit milestone SSE events during generation

Currently `OnProgress` fires only after `dispatchResource()` returns. Add milestone events at each phase transition so the MCP client gets real-time updates:

```go
func (s *Spec) dispatchResource(...) *DispatchResult {
    emitProgress("queued")
    ctxResult, err := s.Context(...)
    emitProgress("generating")
    loopResult, err := runConstraintLoop(...)
    emitProgress("committing")
    commitResult, err := s.Commit(...)
    emitProgress("committed")
}
```

### Fix 6: Reconcile resource counts

Either:
- **Option A**: Include infrastructure resources in `wave_status` with a `kind: "infrastructure"` flag so the count matches `spec/status`
- **Option B**: Exclude infrastructure resources from the `spec/status` total so the count matches `wave_status`

## Priority

1. **Fix 2** (incremental attempts) — smallest change, biggest diagnostic value
2. **Fix 3** (timing) — essential for stall detection
3. **Fix 1** (sub-states/phase) — makes wave_status actually useful
4. **Fix 5** (SSE milestones) — enables real-time dashboard
5. **Fix 4** (semaphore) — nice-to-have for capacity planning
6. **Fix 6** (counts) — cosmetic but reduces confusion
