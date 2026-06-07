# SP5: Spec Engine & Constraint Loop — Design Spec

## Goal

Build the spec engine: the orchestration layer that drives the plan/apply lifecycle, constraint loop, wave-based execution, sub-agent state machine, and all 23 spec/* MCP tool handlers. This wires together everything from SP1–SP4 into a working system.

## Architecture

```
MCP Tool Handlers (spec/plan, spec/apply, spec/begin, etc.)
    │
    v
Spec Engine (internal/spec/)
    │
    ├── Plan: CUE load → Registry → Graph → Planner → PlannedAction[]
    ├── Apply: wave execution, concurrency, state machine
    ├── Constraint Loop: generate → parse → validate → review → retry
    ├── Session: interactive mode state (begin/next/context/commit/finish)
    └── Resolution: resolve/amend/skip for blocked/errored resources
    │
    v
Engine (internal/engine/) — Generate, Review, CodeReview, Bugbot
Store (internal/store/) — applies, generations, sessions, notes
Prompt (internal/prompt/) — BuildSystemPrompt, BuildResourcePrompt, etc.
```

---

## 1. Store Layer Extensions

### 1.1 New SQL Queries

**`sql/queries/applies.sql`** — CRUD for applies and apply_actions tables:

- `CreateApply(id, spec_hash, status)` — insert a new apply record
- `GetApply(id)` — fetch by ID
- `UpdateApplyStatus(id, status, done_at)` — transition status
- `ListApplies(limit)` — recent applies ordered by started_at DESC
- `CreateApplyAction(apply_id, resource_id, action, status)` — record per-resource action
- `UpdateApplyAction(apply_id, resource_id, status, output)` — update action outcome
- `ListApplyActions(apply_id)` — all actions for an apply

**`sql/queries/generations.sql`** — CRUD for generations table:

- `CreateGeneration(id, apply_id, resource_id, prompt_text, prompt_hash, model, retry_count)` — record an LLM invocation
- `UpdateGeneration(id, output_text, outcome, rejection_reason, duration_ms, input_tokens, output_tokens, cost_usd)` — record completion
- `ListGenerations(resource_id, limit)` — generation history for a resource
- `GetGeneration(id)` — fetch by ID

**`sql/queries/sessions.sql`** — CRUD for agent_sessions and agent_notes:

- `CreateSession(id, apply_id, plan_json, waves_json, hashes_json, status)` — create session
- `GetSession(id)` — fetch by ID
- `GetActiveSession()` — fetch the session with status='active'
- `UpdateSession(id, status, current_wave)` — update session state
- `CreateNote(resource_id, apply_id, content)` — save agent note
- `GetNote(resource_id, apply_id)` — fetch note for a resource
- `ListNotes(apply_id)` — all notes for an apply
- `GetNotesByDependencies(resource_ids)` — notes for a set of dependency IDs

### 1.2 Store Methods

After running sqlc, add these methods to `internal/store/store.go`:

**Apply operations:**
- `CreateApply(id, specHash string) error`
- `GetApply(id string) (*Apply, error)`
- `CompleteApply(id string) error`
- `FailApply(id string) error`
- `ListApplies(limit int) ([]Apply, error)`
- `RecordAction(applyID, resourceID, action, status string) error`
- `UpdateAction(applyID, resourceID, status, output string) error`
- `ListActions(applyID string) ([]ApplyAction, error)`

**Generation operations:**
- `RecordGeneration(gen Generation) error`
- `UpdateGeneration(id string, result GenerationResult) error`
- `ListGenerations(resourceID string, limit int) ([]Generation, error)`

**Session operations:**
- `CreateSession(session Session) error`
- `GetSession(id string) (*Session, error)`
- `GetActiveSession() (*Session, error)`
- `UpdateSession(id, status string, currentWave int) error`
- `SetNote(resourceID, applyID, content string) error`
- `GetNote(resourceID, applyID string) (string, error)`
- `ListNotes(applyID string) ([]AgentNote, error)`
- `GetNotesByDeps(resourceIDs []string) (map[string]string, error)`

### 1.3 Store Domain Types

```go
type Apply struct {
    ID        string
    SpecHash  string
    Status    string // "running", "completed", "failed"
    StartedAt time.Time
    DoneAt    *time.Time
}

type ApplyAction struct {
    ApplyID    string
    ResourceID string
    Action     string // "create", "modify", "destroy"
    Status     string // "pending", "completed", "failed", "skipped"
    Output     string
}

type Generation struct {
    ID              string
    ApplyID         string
    ResourceID      string
    PromptText      string
    PromptHash      string
    OutputText      string
    Model           string
    RetryCount      int
    Outcome         string // "accepted", "rejected"
    RejectionReason string
    DurationMS      int64
    InputTokens     int64
    OutputTokens    int64
    CostUSD         float64
}

type GenerationResult struct {
    OutputText      string
    Outcome         string
    RejectionReason string
    DurationMS      int64
    InputTokens     int64
    OutputTokens    int64
    CostUSD         float64
}

type Session struct {
    ID          string
    ApplyID     string
    PlanJSON    string
    WavesJSON   string
    HashesJSON  string
    Status      string // "active", "completed", "aborted"
    CurrentWave int
}

type AgentNote struct {
    ResourceID string
    ApplyID    string
    Content    string
}
```

---

## 2. Package: `internal/spec/`

### 2.1 Code Block Parser (`parse.go`)

```go
func ParseCodeBlocks(output string) ([]CodeBlock, error)
```

Extracts fenced code blocks with path annotations from LLM output. Looks for:
- `// path: <filepath>` inside a fenced block
- `# path: <filepath>` inside a fenced block
- Language-annotated fences: ` ```rust`, ` ```go`, etc.

```go
type CodeBlock struct {
    Path    string
    Content string
    Lang    string
}
```

Returns error if no code blocks found (parse failure → retry).

### 2.2 Validation Runner (`validate.go`)

```go
type ValidationResult struct {
    Passed  bool
    Kind    string // "compiles", "test", "integration", "custom", "invariant", "review"
    Message string
}

func RunValidations(ctx context.Context, validations []cue.Validation, cwd string) ([]ValidationResult, error)
func RunCommand(ctx context.Context, command []string, cwd string) (stdout, stderr string, exitCode int, err error)
func CheckAssertions(assertions []cue.Assertion, stdout, stderr string, exitCode int) []ValidationResult
```

- `RunValidations` executes each validation in order, stops on first failure
- `RunCommand` runs a subprocess with timeout, captures stdout/stderr
- `CheckAssertions` checks integration assertions (exit_code, file_exists, stdout_contains, etc.)
- For resources with no validations, falls back to config `TypeCheckCommand` and `TestCommand`

### 2.3 Invariant Checker (`invariant.go`)

```go
func CheckInvariants(ctx context.Context, eng engine, code string, invariants []cue.Invariant, model string) ([]ValidationResult, error)
```

Dispatches an LLM call via `engine.Review` with each invariant's text and rationale. The LLM checks whether the generated code violates the invariant. Returns PASS/FAIL per invariant with explanation.

### 2.4 Constraint Loop (`loop.go`)

```go
type LoopResult struct {
    Files           []CodeBlock
    Outcome         string // "accepted", "rejected"
    RejectionReason string
    Attempts        int
    Generations     []Generation
}

func (s *Spec) runConstraintLoop(ctx context.Context, resource cue.Resource, registry *cue.Registry, opts LoopOpts) (*LoopResult, error)
```

The core loop per SPEC.md section 8.2:

1. **Generate** — `engine.Generate(ctx, prompt, systemPrompt, model)`
2. **Parse** — `ParseCodeBlocks(output)` — no blocks → retry
3. **Write temp files** — write blocks to disk for validation
4. **Resource Validations** — `RunValidations(ctx, resource.Validations, cwd)`
5. **Invariant Check** — `CheckInvariants(ctx, eng, code, project.Invariants, model)`
6. **Code Review** — based on `meta.reviewLevel`:
   - `"full"` → `engine.CodeReview`
   - `"light"` → `engine.Bugbot`
   - `"solid"` → `engine.Review`
   - `"skip"` → no review
7. **On failure** — build fix prompt via `prompt.BuildFixPrompt`, retry up to `maxRetries`
8. **On success** — return accepted files

```go
type LoopOpts struct {
    SystemPrompt string
    Prompt       string
    Model        string
    MaxRetries   int
    ReviewLevel  string
    Cwd          string
    RuntimeCtx   prompt.RuntimeContext
}
```

### 2.5 Resource State Machine (`state.go`)

```go
type ResourceState string

const (
    StatePending    ResourceState = "pending"
    StateDispatched ResourceState = "dispatched"
    StateCompleted  ResourceState = "completed"
    StateCommitted  ResourceState = "committed"
    StateBlocked    ResourceState = "blocked"
    StateErrored    ResourceState = "errored"
    StateTimedOut   ResourceState = "timed_out"
    StateRejected   ResourceState = "rejected"
    StateSkipped    ResourceState = "skipped"
)

type ResourceStatus struct {
    ResourceID  string
    State       ResourceState
    WaveIndex   int
    Error       *ErrorContext
    Blocked     *BlockedContext
    Attempts    int
    MaxRetries  int
    Files       []CodeBlock
    Notes       string
    UserGuidance string
}

type ErrorContext struct {
    Kind        string // "compile", "invariant", "runtime", "parse", "review", "validation"
    Message     string
    Files       []string
    RetryCount  int
    MaxRetries  int
    LastAttempt string
    Suggestion  string
}

type BlockedContext struct {
    Reason    string
    BlockedOn string
    Question  string
    Options   []string
}
```

### 2.6 Spec Engine (`spec.go`)

The central orchestrator.

```go
type specEngine interface {
    Generate(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error)
    Review(ctx context.Context, opts engine.ReviewOpts) (*agent.RunResult, error)
    CodeReview(ctx context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error)
    Bugbot(ctx context.Context, opts engine.BugbotOpts) (*agent.RunResult, error)
}

type specStore interface {
    // All store methods needed by spec engine
    GetResource(id string) (*store.Resource, error)
    ListResources() ([]store.Resource, error)
    SetResource(r store.Resource) error
    DeleteResource(id string) error
    GetGeneratedFiles(resourceID string) ([]store.GeneratedFile, error)
    SetGeneratedFile(f store.GeneratedFile) error
    DeleteGeneratedFiles(resourceID string) error
    SetDependency(d store.Dependency) error
    DeleteDependencies(resourceID string) error
    AcquireLock(holder string) error
    ReleaseLock() error
    GetLock() (*store.Lock, error)
    CreateApply(id, specHash string) error
    CompleteApply(id string) error
    FailApply(id string) error
    ListApplies(limit int) ([]store.Apply, error)
    RecordAction(applyID, resourceID, action, status string) error
    UpdateAction(applyID, resourceID, status, output string) error
    ListActions(applyID string) ([]store.ApplyAction, error)
    RecordGeneration(gen store.Generation) error
    UpdateGeneration(id string, result store.GenerationResult) error
    ListGenerations(resourceID string, limit int) ([]store.Generation, error)
    CreateSession(session store.Session) error
    GetSession(id string) (*store.Session, error)
    GetActiveSession() (*store.Session, error)
    UpdateSession(id, status string, currentWave int) error
    SetNote(resourceID, applyID, content string) error
    GetNote(resourceID, applyID string) (string, error)
    ListNotes(applyID string) ([]store.AgentNote, error)
    GetNotesByDeps(resourceIDs []string) (map[string]string, error)
}

type fileSystem interface {
    ReadFile(path string) ([]byte, error)
    WriteFile(path string, data []byte, perm os.FileMode) error
    MkdirAll(path string, perm os.FileMode) error
    Remove(path string) error
    ReadDir(path string) ([]os.DirEntry, error)
}

type Spec struct {
    engine  specEngine
    store   specStore
    fs      fileSystem
    cfg     *config.Config
}

func New(engine specEngine, store specStore, fs fileSystem, cfg *config.Config) *Spec
```

### 2.7 Plan Operation (`plan.go` in spec package)

```go
func (s *Spec) Plan(ctx context.Context) (*PlanResult, error)
```

Loads CUE from `cfg.SpecDir`, builds registry, graph, runs planner against store. Returns structured plan.

```go
type PlanResult struct {
    Actions  []plan.PlannedAction
    Registry *cue.Registry
    Graph    *graph.Graph
    Waves    [][]string
    Hashes   map[string]string
}
```

### 2.8 Apply Operation (`apply.go`)

```go
func (s *Spec) Apply(ctx context.Context, opts ApplyOpts) (*ApplyResult, error)
```

The automated apply:

1. Run `Plan(ctx)` to get actions
2. Acquire exclusive lock
3. Create apply record
4. Phase A: execute destroys (delete files, remove state)
5. Phase B: execute creates/modifies via wave execution
   - For each wave: dispatch resources concurrently, run constraint loop per resource
   - Between waves: wave-level verification (type check + tests)
   - On wave failure: attribute errors to resources, retry failed resources
6. Commit state for all accepted resources
7. Release lock, complete apply record

```go
type ApplyOpts struct {
    Target string // optional: filter to single resource + dependents
    Force  bool   // bypass hash-based skip
    Model  string // override generate model
    DryRun bool   // plan only, don't execute
}

type ApplyResult struct {
    ApplyID   string
    Status    string
    Actions   []ActionResult
    Summary   ApplySummary
}

type ActionResult struct {
    ResourceID string
    Action     string
    Status     string // "completed", "failed", "skipped"
    Files      []string
    Error      string
}

type ApplySummary struct {
    Created   int
    Modified  int
    Destroyed int
    Failed    int
    Skipped   int
    Duration  time.Duration
}
```

### 2.9 Interactive Session (`session.go`)

```go
func (s *Spec) Begin(ctx context.Context, opts BeginOpts) (*BeginResult, error)
func (s *Spec) Next(ctx context.Context, sessionID string) (*NextResult, error)
func (s *Spec) Context(ctx context.Context, sessionID, resourceID string) (*ContextResult, error)
func (s *Spec) Commit(ctx context.Context, sessionID, resourceID string, files []CommitFile, notes string) error
func (s *Spec) Finish(ctx context.Context, sessionID string, force bool) (*FinishResult, error)
```

**Begin:** Plans, creates session, acquires lock, returns plan + orchestrator instructions.

```go
type BeginOpts struct {
    Target string
    Force  bool
    Model  string
}

type BeginResult struct {
    SessionID    string
    Plan         []plan.PlannedAction
    Waves        [][]string
    Instructions string // orchestrator protocol text
    DriftActions []plan.PlannedAction // drift resources needing resolution
}
```

**Next:** Returns the next wave of uncommitted resources with their states.

```go
type NextResult struct {
    Done      bool
    WaveIndex int
    Resources []ResourceStatus
}
```

**Context:** Builds the full prompt for a resource, including runtime context (module tree, dependency files, agent notes).

```go
type ContextResult struct {
    SystemPrompt      string
    Prompt            string
    DependencyNotes   map[string]string
    Instructions      string // per-resource dispatch instructions
}
```

**Commit:** Records a resource as complete — saves files, updates state, records generation.

```go
type CommitFile struct {
    Path    string
    Content string
}
```

**Finish:** Releases lock, completes session, returns summary.

```go
type FinishResult struct {
    Summary ApplySummary
    Skipped []string
    Errors  []string
}
```

### 2.10 Resolution Operations (`resolve.go`)

```go
func (s *Spec) Resolve(ctx context.Context, sessionID, resourceID, answer string, model string) error
func (s *Spec) Amend(ctx context.Context, sessionID, resourceID string) error
func (s *Spec) Skip(ctx context.Context, sessionID, resourceID, reason string) error
```

- **Resolve:** Stores user guidance in agent_notes, re-dispatches resource with guidance in prompt
- **Amend:** Re-loads CUE spec, re-computes hashes, updates in-flight plan, re-dispatches
- **Skip:** Transitions resource to `skipped`, wave proceeds without it

### 2.11 Query Operations (`query.go`)

Read-only operations for inspection:

```go
func (s *Spec) Status(ctx context.Context) (*StatusResult, error)     // current state overview
func (s *Spec) Log(ctx context.Context, limit int) ([]store.Apply, error)  // past applies
func (s *Spec) History(ctx context.Context, resourceID string, limit int) ([]store.Generation, error)
func (s *Spec) GraphInfo(ctx context.Context) (*GraphResult, error)   // dependency graph
func (s *Spec) Diff(ctx context.Context, applyA, applyB string) (*DiffResult, error)
func (s *Spec) State(ctx context.Context, action, resourceID string) (*StateResult, error)
func (s *Spec) Validate(ctx context.Context) (*ValidateResult, error) // structural invariant check
func (s *Spec) ValidateResource(ctx context.Context, resourceID string) (*ValidateResourceResult, error)
func (s *Spec) DriftAction(ctx context.Context, action, resourceID string) error // accept/revert drift
func (s *Spec) Vacuum(ctx context.Context, before time.Time) error
func (s *Spec) Unlock(ctx context.Context) error
```

### 2.12 Runtime Context Builder (`runtime.go`)

Populates `prompt.RuntimeContext` by reading actual files and state:

```go
func (s *Spec) buildRuntimeContext(ctx context.Context, resource cue.Resource, registry *cue.Registry, applyID string) (prompt.RuntimeContext, error)
```

- **ModuleTree:** Scans `src/` directory, builds tree listing
- **DependencyFiles:** For each dependency, reads generated files from disk (excludes test files)
- **AgentNotes:** Fetches notes from store for dependency resources
- **WaveErrors:** Populated by wave execution when retrying after wave-level failure
- **UserGuidance:** From `spec/resolve` — stored in agent_notes

---

## 3. MCP Tool Handlers

All 23 spec/* tool handlers delegate to `Spec` methods. They live in `internal/mcp/` and replace the current stubs.

### 3.1 Sync Tools

| Tool | Handler delegates to | Returns |
|------|---------------------|---------|
| `spec/plan` | `spec.Plan(ctx)` | JSON: planned actions with reasons |
| `spec/validate` | `spec.Validate(ctx)` | JSON: invariant check results |
| `spec/begin` | `spec.Begin(ctx, opts)` | JSON: session ID, plan, waves, instructions |
| `spec/next` | `spec.Next(ctx, sessionID)` | JSON: wave resources with states, or done=true |
| `spec/context` | `spec.Context(ctx, sessionID, resourceID)` | JSON: system prompt, resource prompt, notes |
| `spec/validate-resource` | `spec.ValidateResource(ctx, resourceID)` | JSON: validation results |
| `spec/note` | `spec.store.SetNote(...)` | JSON: confirmation |
| `spec/commit` | `spec.Commit(ctx, sessionID, resourceID, files, notes)` | JSON: confirmation |
| `spec/resolve` | `spec.Resolve(ctx, sessionID, resourceID, answer, model)` | JSON: confirmation |
| `spec/amend` | `spec.Amend(ctx, sessionID, resourceID)` | JSON: updated plan |
| `spec/skip` | `spec.Skip(ctx, sessionID, resourceID, reason)` | JSON: confirmation |
| `spec/finish` | `spec.Finish(ctx, sessionID, force)` | JSON: summary |
| `spec/status` | `spec.Status(ctx)` | JSON: resources, session, lock |
| `spec/log` | `spec.Log(ctx, limit)` | JSON: past applies |
| `spec/history` | `spec.History(ctx, resourceID, limit)` | JSON: generation history |
| `spec/graph` | `spec.GraphInfo(ctx)` | JSON: dependency graph |
| `spec/diff` | `spec.Diff(ctx, applyA, applyB)` | JSON: state delta |
| `spec/state` | `spec.State(ctx, action, resourceID)` | JSON: state info or rm confirmation |
| `spec/drift` | `spec.DriftAction(ctx, action, resourceID)` | JSON: confirmation |
| `spec/vacuum` | `spec.Vacuum(ctx, before)` | JSON: rows deleted |
| `spec/sql` | (not implementable via MCP — returns instructions) | text |
| `spec/unlock` | `spec.Unlock(ctx)` | JSON: confirmation |

### 3.2 Async Tools

| Tool | Handler | Returns |
|------|---------|---------|
| `spec/apply` | `runAsync("spec/apply", func() { spec.Apply(ctx, opts) })` | Job ID (async) |

`spec/apply` is the only async spec tool. It runs the full apply pipeline in a background goroutine, using the existing async job model. Progress notifications are emitted at wave boundaries.

### 3.3 MCP Resources

Implement `handleResourcesList` and `handleResourcesRead` to serve:

| URI | Delegates to |
|-----|-------------|
| `crest-spec://plan` | `spec.Plan(ctx)` |
| `crest-spec://state` | `spec.Status(ctx)` |
| `crest-spec://graph` | `spec.GraphInfo(ctx)` |
| `crest-spec://session` | `spec.store.GetActiveSession()` |
| `crest-spec://metrics` | existing `metrics.snapshot()` |

### 3.4 MCP Prompts

Implement `handlePromptsList` and `handlePromptsGet`:

| Name | Delegates to |
|------|-------------|
| `system_prompt` | `prompt.BuildSystemPrompt(project)` |
| `resource_prompt` | `prompt.BuildResourcePrompt(resource, registry)` |
| `orchestrator_instructions` | static text from SPEC.md 9.5 |

---

## 4. File System Abstraction

A thin interface for testability:

```go
type OSFileSystem struct{}

func (OSFileSystem) ReadFile(path string) ([]byte, error)    { return os.ReadFile(path) }
func (OSFileSystem) WriteFile(path string, data []byte, perm os.FileMode) error { return os.WriteFile(path, data, perm) }
func (OSFileSystem) MkdirAll(path string, perm os.FileMode) error { return os.MkdirAll(path, perm) }
func (OSFileSystem) Remove(path string) error                { return os.Remove(path) }
func (OSFileSystem) ReadDir(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }
```

The `fileSystem` interface is consumed by `Spec`, `Planner` (already uses `fileReader`), and the runtime context builder.

---

## 5. Wiring Changes

### 5.1 `cmd/crest-spec/main.go`

Update startup sequence to create `Spec` and pass it to MCP server:

```go
specEngine := spec.New(eng, s, spec.OSFileSystem{}, cfg)
srv := mcp.New(specEngine, eng, s, mcp.OSProcessTree{}, os.Stdin, os.Stdout, log, cfg)
```

### 5.2 `internal/mcp/server.go`

Add `spec *spec.Spec` field to Server. Update `New()` to accept it. Tool handlers delegate to spec methods.

---

## 6. Testing Strategy

### 6.1 Unit Tests

- **parse_test.go** — code block extraction: single block, multiple blocks, path annotations, no blocks, malformed fences
- **validate_test.go** — command execution, assertion checking, validation sequencing
- **invariant_test.go** — invariant check prompt construction (mock engine)
- **loop_test.go** — constraint loop: pass on first try, retry on parse failure, retry on validation failure, max retries exhausted (mock engine)
- **state_test.go** — resource state transitions: valid and invalid
- **session_test.go** — begin/next/commit/finish lifecycle (mock store + engine)
- **resolve_test.go** — resolve/amend/skip operations
- **plan_spec_test.go** — plan operation end-to-end with test CUE fixtures
- **query_test.go** — read-only query operations

### 6.2 Store Tests

- Apply CRUD, action recording, generation tracking
- Session lifecycle, note storage
- All new store methods tested against real SQLite

### 6.3 Integration Tests

- Full pipeline: CUE load → plan → apply with mock engine
- Wave execution with dependency ordering
- Constraint loop retry behavior
- Interactive session lifecycle (begin → next → context → commit → finish)

---

## 7. What SP5 Does NOT Include

- **Real LLM calls in tests** — unit tests use mock engine; real e2e testing is manual
- **CLI subcommands** beyond `check job` — `state list`, `state rm`, `diff`, `vacuum`, `sql` are future work (the MCP tools cover the same functionality)
- **Production hardening** — timeouts, rate limiting, graceful degradation are follow-on concerns

---

## 8. File Structure

### New files

| File | Responsibility |
|------|---------------|
| `sql/queries/applies.sql` | Apply and action queries |
| `sql/queries/generations.sql` | Generation tracking queries |
| `sql/queries/sessions.sql` | Session and note queries |
| `internal/spec/spec.go` | Spec engine struct, constructor, interfaces |
| `internal/spec/parse.go` | Code block parser |
| `internal/spec/validate.go` | Validation runner |
| `internal/spec/invariant.go` | Invariant checker |
| `internal/spec/loop.go` | Constraint loop |
| `internal/spec/state.go` | Resource state machine |
| `internal/spec/plan.go` | Plan operation |
| `internal/spec/apply.go` | Apply operation (automated) |
| `internal/spec/session.go` | Interactive session (begin/next/context/commit/finish) |
| `internal/spec/resolve.go` | Resolution operations (resolve/amend/skip) |
| `internal/spec/query.go` | Read-only query operations |
| `internal/spec/runtime.go` | Runtime context builder |
| `internal/spec/fs.go` | File system abstraction |

### Modified files

| File | Changes |
|------|---------|
| `internal/store/store.go` | Add apply, generation, session, note methods |
| `internal/mcp/server.go` | Add spec field, pass to constructor |
| `internal/mcp/tools.go` | Replace 23 spec/* stubs with real handlers |
| `internal/mcp/handlers.go` | Implement resources/read, resources/list, prompts/list, prompts/get |
| `cmd/crest-spec/main.go` | Create Spec, pass to MCP server |
