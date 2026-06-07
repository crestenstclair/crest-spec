# crest-spec Sub-project 2: Engine + MCP Server

> Design specification for the engine and MCP server layers of crest-spec. Builds on the
> foundation established in SP1 (errors, config, store, agent, app, main.go).

## Decomposition Overview

| Sub-project | Scope | Depends on |
|-------------|-------|------------|
| 1. Foundation | errors, config, app, migrations, sqlc, store, agent, main.go | - |
| **2. Engine + MCP** (this) | engine (Generate, Review, CodeReview, Bugbot), mcp server, engine tools, main.go wiring | SP1 |
| 3. CUE Loader + Graph + Planner | cue loader, resource graph, planner, basic spec tools | SP1, SP2 |
| 4. Prompt Builder | system prompt, resource prompt, fix prompt, context injection | SP3 |
| 5. Spec Engine + Constraint Loop | plan/apply lifecycle, wave execution, constraint loop, state machine, verify | SP1-SP4 |

## Goal

Deliver two packages -- `internal/engine` and `internal/mcp` -- that together provide the sub-agent dispatch and MCP server infrastructure. After SP2, the binary is a functioning MCP server that can:

1. Accept JSON-RPC requests over stdio and (optionally) HTTP.
2. Dispatch constrained code generation, review, code review, and bugbot operations via the claude CLI.
3. Run those operations asynchronously with SQLite-persisted job state.
4. Expose all engine tools (`run_prompt`, `code_review`, `bugbot`, `poll_result`, `cancel_job`, `list_jobs`, `list_models`, `about`, `status`, `live_metrics`).
5. Register spec tool stubs (names and descriptions only; implementations deferred to SP3-SP5).
6. Enforce concurrency limits, detect recursion, and collect per-tool metrics.

Spec tools (`spec/plan`, `spec/apply`, etc.) return `"not implemented yet"` -- their real implementations arrive in SP3-SP5.

## Directory Layout (new files)

```
crest-spec/
├── internal/
│   ├── engine/
│   │   ├── engine.go
│   │   └── engine_test.go
│   ├── mcp/
│   │   ├── server.go
│   │   ├── tools.go
│   │   ├── handlers.go
│   │   ├── metrics.go
│   │   ├── recursion.go
│   │   ├── process.go
│   │   └── server_test.go
│   ├── mocks/
│   │   ├── fake_runner.go       (counterfeiter, from engine.runner)
│   │   ├── fake_engine.go       (counterfeiter, from mcp.engine)
│   │   ├── fake_store.go        (counterfeiter, from mcp.store)
│   │   └── fake_process_tree.go (counterfeiter, from mcp.processTree)
├── cmd/crest-spec/main.go       (updated)
```

---

## Package Designs

### 1. `internal/engine` -- Sub-Agent Dispatch

The engine wraps the agent with higher-level operations. It owns the concurrency semaphore and provides the execution paths that the MCP server and (later) the spec layer call.

#### Package-Private Interface: `runner`

Defined inside `engine.go`. This is the surface of `agent.Agent` that the engine depends on. The real Agent satisfies it implicitly; tests use a counterfeiter fake.

```go
type runner interface {
    RunPrompt(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error)
    Models(ctx context.Context) (string, error)
    About(ctx context.Context) (string, error)
    Status(ctx context.Context) (string, error)
}
```

#### `Engine` struct

```go
type Engine struct {
    r    runner
    store engineStore
    cfg  *config.Config
    sem  chan struct{}
}
```

**Fields:**

- `r` -- the runner interface (backed by `agent.Agent` in production).
- `store` -- not used directly by engine operations in SP2, but carried for SP5 when the spec engine needs store access through the engine. Type is a package-private `engineStore` interface (initially empty; expanded in SP5).
- `cfg` -- full config for reading model defaults, concurrency, etc.
- `sem` -- buffered channel of size `cfg.MaxConcurrency`. All operations acquire a slot before spawning a claude subprocess and release it when done.

#### Constructor

```go
func New(r runner, s engineStore, cfg *config.Config) *Engine
```

Creates the semaphore: `make(chan struct{}, cfg.MaxConcurrency)`.

#### Semaphore Helpers

```go
func (e *Engine) acquire(ctx context.Context) error
```

Attempts to send on `e.sem`. Uses `select` with `ctx.Done()` for cancellable acquire. Returns `ctx.Err()` if the context is cancelled before a slot is available.

```go
func (e *Engine) release()
```

Receives from `e.sem`.

#### Operations

All operations follow the pattern: acquire semaphore, build `RunOpts`, call `e.r.RunPrompt`, release semaphore, return result.

##### Generate

```go
func (e *Engine) Generate(ctx context.Context, prompt, systemPrompt, model string) (*agent.RunResult, error)
```

Constrained code generation. No tool access, stateless.

**Behavior:**
1. Acquire semaphore (cancellable).
2. Resolve model: use `model` if non-empty, else `e.cfg.GenerateModel`.
3. Build `RunOpts`:
   - `Prompt`: the provided prompt
   - `Model`: resolved model
   - `DisallowedTools`: `["Bash", "Read", "Edit", "Write", "Glob", "Grep", "WebFetch", "WebSearch"]`
   - `NoSessionPersistence`: `true`
   - `AppendSystemPrompt`: the provided systemPrompt
4. Call `e.r.RunPrompt(ctx, opts)`.
5. Release semaphore.
6. Return `(*RunResult, error)`.

##### Review

```go
func (e *Engine) Review(ctx context.Context, code, requirements, model string) (*agent.RunResult, error)
```

LLM verification pass. Checks generated code against requirements.

**Behavior:**
1. Acquire semaphore.
2. Resolve model: use `model` if non-empty, else `e.cfg.VerifyModel`.
3. Build prompt: a review template that includes `code` and `requirements`, asking the LLM to check SOLID principles, folder structure, dependency injection, interfaces, tests, and declared invariants. The prompt ends with: "Reply with PASS if the code meets all requirements, or FAIL followed by specific issues."
4. Build `RunOpts`:
   - `Prompt`: the built review prompt
   - `Model`: resolved model
   - `DisallowedTools`: same as Generate (no tool access)
   - `NoSessionPersistence`: `true`
5. Call `e.r.RunPrompt(ctx, opts)`.
6. Release semaphore.
7. Return `(*RunResult, error)`. The caller parses for PASS/FAIL.

##### CodeReview

```go
func (e *Engine) CodeReview(ctx context.Context, cwd string, models []string, prompt string) (string, error)
```

Multi-model code review. Fans out across models, aggregates results per model.

**Behavior:**
1. Default models to `["claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-3-5"]` if empty.
2. Create an `errgroup.Group` with the provided context.
3. For each model, launch a goroutine:
   a. Acquire semaphore (cancellable via group context).
   b. Build `RunOpts` with the review prompt, `Cwd` set, `NoSessionPersistence: true`, the specific model.
   c. Call `e.r.RunPrompt`.
   d. Release semaphore.
   e. Collect result into a thread-safe slice (mutex-guarded).
4. Wait for all goroutines.
5. Aggregate: build a string with `## Model: <name>\n\n<output>\n\n` sections.
6. Return aggregated string, first error (if any).

##### Bugbot

```go
func (e *Engine) Bugbot(ctx context.Context, cwd string, models []string, prompt string) (string, error)
```

Lightweight severity-ranked scan. Faster than CodeReview, uses cheaper models by default.

**Behavior:**
1. Default models to `["claude-haiku-3-5"]` if empty.
2. Same fan-out pattern as CodeReview.
3. The prompt template instructs the LLM to find bugs, rank by severity (critical/high/medium/low), and provide a one-line remedy for each.
4. Return aggregated results, first error.

##### Pass-Through Methods

These delegate directly to the runner without acquiring the semaphore (they are lightweight, non-subprocess calls):

```go
func (e *Engine) Models(ctx context.Context) (string, error)
func (e *Engine) About(ctx context.Context) (string, error)
func (e *Engine) Status(ctx context.Context) (string, error)
```

---

### 2. `internal/mcp` -- MCP Server

The JSON-RPC server that exposes engine operations (and eventually spec operations) as MCP tools.

#### Package-Private Interfaces

Defined in `server.go`. Each is the consumer-side surface of a dependency.

##### `engine` interface

```go
type engine interface {
    Generate(ctx context.Context, prompt, systemPrompt, model string) (*agent.RunResult, error)
    Review(ctx context.Context, code, requirements, model string) (*agent.RunResult, error)
    CodeReview(ctx context.Context, cwd string, models []string, prompt string) (string, error)
    Bugbot(ctx context.Context, cwd string, models []string, prompt string) (string, error)
    Models(ctx context.Context) (string, error)
    About(ctx context.Context) (string, error)
    Status(ctx context.Context) (string, error)
}
```

##### `store` interface

```go
type store interface {
    CreateJob(id, tool string, pid int) error
    CompleteJob(id, result string) error
    FailJob(id string, jobErr error) error
    CancelJob(id string) error
    GetJob(id string) (*storemod.Job, error)
    ListJobs(limit int) ([]storemod.Job, error)
    DeleteJob(id string) error
    CleanupOrphans(aliveFn func(int) bool) (int, error)
}
```

##### `processTree` interface

```go
type processTree interface {
    ParentProcess(pid int) (name string, ppid int, err error)
    SelfPID() int
}
```

#### `OSProcessTree` struct

The real implementation of `processTree`. Lives in `process.go`.

```go
type OSProcessTree struct{}

func (OSProcessTree) SelfPID() int
func (OSProcessTree) ParentProcess(pid int) (string, int, error)
```

`SelfPID()` returns `os.Getpid()`.

`ParentProcess(pid)` uses `exec.Command("ps", "-o", "comm=,ppid=", "-p", strconv.Itoa(pid))` to get the command name and parent PID. Parses the output. Returns an error if the process does not exist.

#### `Server` struct

```go
type Server struct {
    eng       engine
    store     store
    pt        processTree
    stdin     io.Reader
    stdout    io.Writer
    log       zerolog.Logger
    cfg       *config.Config
    metrics   *Metrics
    cancels   map[string]context.CancelFunc
    cancelsMu sync.Mutex
    asyncWg   sync.WaitGroup
    bgCtx     context.Context
    bgCancel  context.CancelFunc
    outMu     sync.Mutex
    tools     []toolDef
    dispatch  map[string]handlerFunc
    startTime time.Time
}
```

**Fields:**

- `eng` -- the engine interface.
- `store` -- the store interface (for job CRUD).
- `pt` -- process tree for recursion detection.
- `stdin`, `stdout` -- stdio transport endpoints.
- `log` -- zerolog logger.
- `cfg` -- config for reading HTTPAddr, etc.
- `metrics` -- per-tool metrics tracker.
- `cancels` -- map of job ID to cancel func, for cancelling in-flight async jobs.
- `cancelsMu` -- guards `cancels`.
- `asyncWg` -- tracks in-flight async goroutines for graceful shutdown.
- `bgCtx`, `bgCancel` -- background context for async jobs (not tied to any single request).
- `outMu` -- mutex serializing writes to stdout.
- `tools` -- registered tool definitions.
- `dispatch` -- map from JSON-RPC method string to handler function.
- `startTime` -- for uptime calculation.

#### Constructor

```go
func New(
    eng engine,
    store store,
    pt processTree,
    stdin io.Reader,
    stdout io.Writer,
    log zerolog.Logger,
    cfg *config.Config,
) *Server
```

1. Creates `bgCtx, bgCancel` from `context.Background()`.
2. Initializes `cancels` map, `metrics`, `startTime`.
3. Calls `s.registerTools()` to build the tool definitions and dispatch map.
4. Runs `DetectRecursion(pt)` -- if recursion is detected, replaces all tools with a single placeholder tool that returns an error message.
5. Returns the server.

#### JSON-RPC Types

```go
type jsonRPCRequest struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      any             `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
    JSONRPC string      `json:"jsonrpc"`
    ID      any         `json:"id"`
    Result  any         `json:"result,omitempty"`
    Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
    Code    int    `json:"code"`
    Message string `json:"message"`
}

type handlerFunc func(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse
```

Standard JSON-RPC 2.0 error codes used:
- `-32700` -- Parse error
- `-32601` -- Method not found
- `-32602` -- Invalid params
- `-32603` -- Internal error

#### Tool Definition Types

```go
type toolDef struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    InputSchema json.RawMessage `json:"inputSchema"`
}

type toolCallParams struct {
    Name      string          `json:"name"`
    Arguments json.RawMessage `json:"arguments"`
    Meta      *toolCallMeta   `json:"_meta,omitempty"`
}

type toolCallMeta struct {
    ProgressToken string `json:"progressToken,omitempty"`
}

type toolResult struct {
    Content []contentBlock `json:"content"`
    IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
    Type string `json:"type"`
    Text string `json:"text"`
}
```

#### Stdio Transport: `Run(ctx context.Context) error`

1. Create a `bufio.Scanner` on `s.stdin` with a 10 MiB buffer (`scanner.Buffer(make([]byte, 0, 10<<20), 10<<20)`).
2. Spin up a reader goroutine:
   - For each line from the scanner:
     - Skip empty/whitespace-only lines.
     - Parse as `jsonRPCRequest`. On parse failure, write a `-32700` error response.
     - Look up handler in `s.dispatch`. If not found, write `-32601`.
     - If method is `tools/call`, check if the tool is async -- if so, the handler itself manages the async lifecycle.
     - Launch `s.asyncWg.Add(1); go func() { defer s.asyncWg.Done(); resp := handler(ctx, req.ID, req.Params); s.writeResponse(resp) }()`.
3. Wait for `ctx.Done()` (signal shutdown).
4. Cancel `s.bgCtx` to stop async jobs.
5. Wait on `s.asyncWg` with a 30-second timeout for in-flight jobs to drain.
6. Return `nil`.

#### `writeResponse(resp jsonRPCResponse)`

Marshals `resp` to JSON, appends `\n`, writes to `s.stdout` under `s.outMu`.

#### HTTP Transport: `ServeHTTP(w http.ResponseWriter, r *http.Request)`

Active when `cfg.HTTPAddr` is set. The server is mounted as a handler on `POST /mcp`.

**Sync tools:**
1. Read request body, parse as `jsonRPCRequest`.
2. Dispatch to handler.
3. Write JSON response with `Content-Type: application/json`.

**Async tools (run_prompt, code_review, bugbot):**
1. Parse request.
2. Set response headers for SSE: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`.
3. Flush the initial headers.
4. The async job's progress callback writes SSE events: `data: {"jsonrpc":"2.0","method":"notifications/progress",...}\n\n`.
5. When the job completes, write the final tool result as an SSE event, then close.

**HTTP server lifecycle** (managed from main.go):
- `http.Server{Addr: cfg.HTTPAddr, Handler: mux}`
- `mux.HandleFunc("POST /mcp", srv.ServeHTTP)`
- Launched in a goroutine with `httpSrv.ListenAndServe()`.
- On context cancellation: `httpSrv.Shutdown(shutdownCtx)` with a 30-second timeout.

#### Request Routing

The `dispatch` map is populated during `registerTools()`:

| Method | Handler |
|--------|---------|
| `initialize` | `handleInitialize` |
| `notifications/initialized` | `handleInitialized` |
| `tools/list` | `handleToolsList` |
| `tools/call` | `handleToolCall` |
| `resources/list` | `handleResourcesList` |
| `resources/read` | `handleResourcesRead` |
| `prompts/list` | `handlePromptsList` |
| `prompts/get` | `handlePromptsGet` |

Any other method returns `-32601 Method not found`.

#### `handleInitialize`

Returns:

```json
{
  "protocolVersion": "2024-11-05",
  "capabilities": {
    "tools": {},
    "resources": {},
    "prompts": {}
  },
  "serverInfo": {
    "name": "crest-spec",
    "version": "0.1.0"
  },
  "instructions": "crest-spec is a declarative code generation system..."
}
```

#### `handleInitialized`

No-op acknowledgment. Returns an empty success response.

#### `handleToolsList`

Returns `{"tools": [<toolDef>, ...]}` -- the full list of registered tools with their names, descriptions, and input schemas.

#### `handleToolCall`

1. Parse `params` as `toolCallParams`.
2. Look up the tool by name in a tool dispatch map (separate from the method dispatch map).
3. If not found, return error content: `"unknown tool: <name>"`.
4. Call the tool handler. The tool handler returns a `toolResult`.
5. Wrap in JSON-RPC response.

#### `handleResourcesList`, `handleResourcesRead`, `handlePromptsList`, `handlePromptsGet`

In SP2, these return empty lists / stubs. Real implementations arrive in SP3+ when the spec layer exists.

- `resources/list` returns `{"resources": []}`.
- `resources/read` returns `-32602` (no resources available yet).
- `prompts/list` returns `{"prompts": []}`.
- `prompts/get` returns `-32602` (no prompts available yet).

---

### 3. Tool Definitions and Handlers

All tools are registered in `tools.go`. Each tool has a name, description, JSON Schema for its input, and a handler function.

#### Engine Tools (fully implemented in SP2)

##### `run_prompt` (async)

**Description:** Run a prompt via the claude CLI sub-agent. Returns a job ID immediately; use `poll_result` to retrieve the output.

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "prompt":        {"type": "string", "description": "The prompt to send"},
    "system_prompt": {"type": "string", "description": "System prompt appended to the agent"},
    "model":         {"type": "string", "description": "Model override (default: generate model from config)"}
  },
  "required": ["prompt"]
}
```

**Handler:** Calls `s.runAsync("run_prompt", func(ctx) (string, error) { return s.eng.Generate(ctx, prompt, systemPrompt, model) })`. Returns `{"job_id": "<uuid>"}`.

##### `code_review` (async)

**Description:** Multi-model code review. Fans out across models and aggregates findings per model.

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "cwd":    {"type": "string", "description": "Working directory for the review"},
    "models": {"type": "array", "items": {"type": "string"}, "description": "Models to use (default: opus, sonnet, haiku)"},
    "prompt": {"type": "string", "description": "Review instructions or focus areas"}
  },
  "required": ["prompt"]
}
```

**Handler:** Calls `s.runAsync("code_review", func(ctx) (string, error) { return s.eng.CodeReview(ctx, cwd, models, prompt) })`. Returns `{"job_id": "<uuid>"}`.

##### `bugbot` (async)

**Description:** Lightweight severity-ranked bug scan.

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "cwd":    {"type": "string", "description": "Working directory for the scan"},
    "models": {"type": "array", "items": {"type": "string"}, "description": "Models to use (default: haiku)"},
    "prompt": {"type": "string", "description": "Scan focus or file list"}
  },
  "required": ["prompt"]
}
```

**Handler:** Calls `s.runAsync("bugbot", func(ctx) (string, error) { return s.eng.Bugbot(ctx, cwd, models, prompt) })`. Returns `{"job_id": "<uuid>"}`.

##### `poll_result` (sync)

**Description:** Check a job's status. Optionally consume (delete) the result.

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "job_id":  {"type": "string", "description": "The job ID to poll"},
    "consume": {"type": "boolean", "description": "If true, delete the job after reading (default: false)"}
  },
  "required": ["job_id"]
}
```

**Handler:**
1. `s.store.GetJob(jobID)`.
2. If not found, return error content.
3. Build response JSON: `{"status": "<status>", "result": "<result>", "error": "<error>"}`.
4. If `consume` is true and job is terminal (`completed`, `failed`, `cancelled`), call `s.store.DeleteJob(jobID)`.
5. Return as text content.

##### `cancel_job` (sync)

**Description:** Cancel a running job and kill its subprocess group.

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "job_id": {"type": "string", "description": "The job ID to cancel"}
  },
  "required": ["job_id"]
}
```

**Handler:**
1. Lock `s.cancelsMu`, look up cancel func in `s.cancels[jobID]`.
2. If found, call the cancel func. This triggers context cancellation in the async goroutine, which will call `store.CancelJob` and kill the subprocess group.
3. If not found, check if the job exists in the store -- it may have already finished. Return appropriate message.
4. Return `{"cancelled": true}` or error content.

##### `list_jobs` (sync)

**Description:** List up to 50 recent non-deleted jobs.

**Input Schema:**
```json
{
  "type": "object",
  "properties": {
    "limit": {"type": "integer", "description": "Max jobs to return (default: 50, max: 50)"}
  }
}
```

**Handler:** Calls `s.store.ListJobs(limit)`. Marshals to JSON array.

##### `list_models` (sync)

**Description:** List available Claude models.

**Input Schema:**
```json
{
  "type": "object",
  "properties": {}
}
```

**Handler:** Calls `s.eng.Models(ctx)`. Returns the output as text content.

##### `about` (sync)

**Description:** Show claude CLI version and auth status.

**Input Schema:**
```json
{
  "type": "object",
  "properties": {}
}
```

**Handler:** Calls `s.eng.About(ctx)` and `s.eng.Status(ctx)`. Combines as `"Version: <about>\nAuth: <status>"`.

##### `status` (sync)

**Description:** Show claude auth status.

**Input Schema:**
```json
{
  "type": "object",
  "properties": {}
}
```

**Handler:** Calls `s.eng.Status(ctx)`. Returns as text content.

##### `live_metrics` (sync)

**Description:** Self-monitoring snapshot: uptime, call counts, error rates, per-tool stats.

**Input Schema:**
```json
{
  "type": "object",
  "properties": {}
}
```

**Handler:** Calls `s.metrics.Snapshot()`. Marshals to JSON.

#### Spec Tool Stubs (registered in SP2, implemented in SP3-SP5)

Each spec tool is registered with its name, description, and input schema. The handler returns a text content response: `"not implemented yet -- available in a future release"`.

Stub tools registered:

| Tool | Description (abbreviated) |
|------|--------------------------|
| `spec/plan` | Show what would change (dry run) |
| `spec/apply` | Execute the plan (async) |
| `spec/validate` | Check structural invariants |
| `spec/begin` | Start interactive agent session |
| `spec/next` | Get next wave of uncommitted resources |
| `spec/context` | Get scoped prompt for a resource |
| `spec/validate-resource` | Run invariant checks for a resource |
| `spec/note` | Save a design decision note |
| `spec/commit` | Record a resource as complete |
| `spec/resolve` | Provide guidance for blocked resource |
| `spec/amend` | Signal spec update for resource |
| `spec/skip` | Skip a failed resource |
| `spec/finish` | Finalize session, release lock |
| `spec/status` | Show current state |
| `spec/log` | List past applies |
| `spec/history` | Show generation history for resource |
| `spec/graph` | Return dependency graph |
| `spec/diff` | Reconstruct state delta between applies |
| `spec/state` | Inspect/modify state tracking |
| `spec/drift` | Handle drifted resources |
| `spec/vacuum` | Compact old history |
| `spec/sql` | Read-only SQLite shell |
| `spec/unlock` | Force-clear stale lock |

Each stub's input schema matches the full schema from SPEC.md section 9.3, so clients can discover parameters even before the tools are functional.

---

### 4. Async Job Model

The async job lifecycle is implemented as a method on `Server`. This is the core pattern shared by `run_prompt`, `code_review`, and `bugbot`.

#### `runAsync`

```go
func (s *Server) runAsync(
    toolName string,
    fn func(ctx context.Context) (string, error),
    progressToken string,
) toolResult
```

**Steps:**

1. Generate a UUID job ID via `uuid.NewString()`.
2. Capture PID via `os.Getpid()`.
3. Create `jobCtx, jobCancel` from `s.bgCtx` (not the request context -- the job outlives the request).
4. Lock `s.cancelsMu`, store `jobCancel` in `s.cancels[id]`.
5. Call `s.store.CreateJob(id, toolName, pid)`. On failure, clean up and return error content.
6. `s.asyncWg.Add(1)`.
7. Launch goroutine:
   ```
   defer s.asyncWg.Done()
   defer jobCancel()
   defer func() { s.cancelsMu.Lock(); delete(s.cancels, id); s.cancelsMu.Unlock() }()

   start := time.Now()
   result, err := fn(jobCtx)
   elapsed := time.Since(start)

   s.metrics.Record(toolName, elapsed, err)

   if err == nil {
       s.store.CompleteJob(id, result)
   } else if jobCtx.Err() != nil {
       s.store.CancelJob(id)
   } else {
       s.store.FailJob(id, err)
   }
   ```
8. Return immediately: `toolResult` with text content `{"job_id": "<id>"}`.

#### Progress Notifications

When a client provides `_meta.progressToken` in a `tools/call` request, async operations can emit progress updates.

For the stdio transport, progress notifications are written as JSON-RPC notifications to stdout:

```json
{
  "jsonrpc": "2.0",
  "method": "notifications/progress",
  "params": {
    "progressToken": "<token>",
    "progress": 50,
    "total": 100,
    "message": "Running code review with claude-opus-4-6..."
  }
}
```

In SP2, progress notifications are emitted at coarse granularity: "job started", "job completed/failed". Finer-grained progress (wave tracking, per-resource updates) arrives with the spec engine in SP5.

---

### 5. Recursion Guard

Lives in `recursion.go`.

```go
func DetectRecursion(pt processTree) bool
```

**Algorithm:**

1. Start at `pt.SelfPID()`.
2. Walk up the process tree via `pt.ParentProcess(pid)`.
3. Maintain a `visited map[int]bool` to prevent infinite loops on self-referential PIDs.
4. Stop when `pid <= 1` or `pid` is already in `visited`.
5. For each ancestor, lowercase the command basename.
6. Count processes whose name contains `"claude"` but does **not** contain `"crest-spec"` and does **not** contain `"mcp"`.
7. If the count exceeds 1, return `true` (recursion detected).

**When recursion is detected:**

The server replaces all tools with a single placeholder:

```go
toolDef{
    Name:        "recursion_detected",
    Description: "This server has detected it is being called recursively. All tools are disabled.",
    InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
}
```

All `tools/call` requests return an error: `"recursion detected: crest-spec tools are disabled to prevent infinite loops"`.

---

### 6. Metrics

Lives in `metrics.go`.

#### `toolMetric` struct

```go
type toolMetric struct {
    Calls  atomic.Int64
    Errors atomic.Int64
    TotalNs atomic.Int64
    MinNs  atomic.Int64
    MaxNs  atomic.Int64
}
```

MinNs is initialized to `math.MaxInt64`. MaxNs is initialized to `0`.

#### `Metrics` struct

```go
type Metrics struct {
    mu        sync.RWMutex
    tools     map[string]*toolMetric
    startTime time.Time
}
```

#### Constructor

```go
func NewMetrics() *Metrics
```

#### `Record(tool string, elapsed time.Duration, err error)`

1. Lock `mu` for read to check if tool exists; upgrade to write lock if not.
2. `m.Calls.Add(1)`.
3. If `err != nil`, `m.Errors.Add(1)`.
4. `m.TotalNs.Add(elapsed.Nanoseconds())`.
5. CAS loop for MinNs: `for { old := m.MinNs.Load(); if ns >= old || m.MinNs.CompareAndSwap(old, ns) { break } }`.
6. CAS loop for MaxNs: `for { old := m.MaxNs.Load(); if ns <= old || m.MaxNs.CompareAndSwap(old, ns) { break } }`.

#### `Snapshot() MetricsSnapshot`

```go
type ToolMetricSnapshot struct {
    Calls  int64   `json:"calls"`
    Errors int64   `json:"errors"`
    AvgMs  float64 `json:"avg_ms"`
    MinMs  float64 `json:"min_ms"`
    MaxMs  float64 `json:"max_ms"`
}

type MetricsSnapshot struct {
    UptimeSeconds float64                       `json:"uptime_seconds"`
    TotalCalls    int64                         `json:"total_calls"`
    TotalErrors   int64                         `json:"total_errors"`
    Tools         map[string]ToolMetricSnapshot `json:"tools"`
}
```

Reads all atomic values under `mu.RLock()`. Converts nanoseconds to milliseconds. Computes `avg_ms = total_ns / calls / 1e6`. If `calls == 0`, all stats are zero.

---

### 7. Updates to `cmd/crest-spec/main.go`

Wire up the engine and MCP server. The current main.go stubs out steps 8-10 with a comment; SP2 fills them in.

#### Changes

After the agent is created (step 7), add:

```go
// Step 8: Engine
eng := engine.New(ag, nil, cfg)  // nil engineStore -- not needed until SP5

// Step 9: MCP Server
srv := mcp.New(eng, s, mcp.OSProcessTree{}, os.Stdin, os.Stdout, log.Logger, cfg)

// Step 10: Transport setup
if cfg.HTTPAddr != "" {
    httpMux := http.NewServeMux()
    httpMux.HandleFunc("POST /mcp", srv.ServeHTTP)
    httpSrv := &http.Server{Addr: cfg.HTTPAddr, Handler: httpMux}
    go func() {
        log.Info().Str("addr", cfg.HTTPAddr).Msg("HTTP transport started")
        if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Error().Err(err).Msg("HTTP server error")
        }
    }()
    go func() {
        <-ctx.Done()
        shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
        defer cancel()
        httpSrv.Shutdown(shutdownCtx)
    }()
}

// Run stdio transport (blocking)
app.New(srv).Run(ctx)
```

The `agent` variable changes from `_` to `ag` so it can be passed to the engine.

The final `<-ctx.Done()` block is replaced by `app.New(srv).Run(ctx)`, which blocks on the stdio transport.

---

## Error Handling

### Engine Errors

- Semaphore acquisition failure (context cancelled): return `(nil, ctx.Err())`.
- RunPrompt failure: return `(partialResult, wrappedError)`. The partial result may contain output from stderr.
- CodeReview/Bugbot fan-out: if some models succeed and others fail, return the aggregated results from successful models and the first error. The caller sees both partial results and the error.

### MCP Server Errors

- JSON parse failure on stdin: write `-32700 Parse error` response, continue reading.
- Unknown method: write `-32601 Method not found` response.
- Invalid params: write `-32602 Invalid params` response.
- Tool handler errors: returned as `toolResult` with `isError: true` and the error message in a text content block. This is an MCP-level tool error, not a JSON-RPC error.
- Job not found (poll_result, cancel_job): return tool error content with the message.
- Store errors (CreateJob, CompleteJob, etc.): logged via zerolog. For CreateJob failure in runAsync, the job is not started and an error tool result is returned. For CompleteJob/FailJob/CancelJob failures, they are logged but not surfaced (the job state may be stale).

### Error Sentinels

No new error sentinels are needed in SP2. The existing `ErrNotFound` and `ErrAlreadyDone` from `internal/errors` cover the store-level cases.

---

## Testing Strategy

### Engine Tests (`engine_test.go`)

Use a counterfeiter `FakeRunner` that implements the `runner` interface.

**Generate:**
- Test that DisallowedTools is set correctly (all 8 tools blocked).
- Test that NoSessionPersistence is true.
- Test that AppendSystemPrompt is passed through.
- Test model defaulting: empty model uses `cfg.GenerateModel`; explicit model overrides.
- Test context cancellation during semaphore acquire.
- Test that runner errors are propagated.

**Review:**
- Test that the review prompt includes both code and requirements.
- Test model defaulting to `cfg.VerifyModel`.

**CodeReview:**
- Test fan-out: fake runner records calls, verify one call per model.
- Test default models are used when models slice is empty.
- Test aggregation: verify output contains sections for each model.
- Test partial failure: one model fails, others succeed -- verify results from successful models are returned with the error.

**Bugbot:**
- Same structure as CodeReview tests but with different default models.

**Semaphore:**
- Test that at most `MaxConcurrency` operations run simultaneously: configure semaphore size 1, launch 2 concurrent operations, verify serialization.

### MCP Server Tests (`server_test.go`)

Use counterfeiter fakes for `engine`, `store`, and `processTree`.

**Stdio Transport:**
- Feed JSON-RPC lines to stdin via a `bytes.Buffer`, collect responses from stdout.
- Test `initialize` returns correct protocol version, capabilities, and server info.
- Test `tools/list` returns all registered tools.
- Test `tools/call` for each engine tool with valid and invalid params.
- Test unknown method returns `-32601`.
- Test malformed JSON returns `-32700`.
- Test concurrent requests are handled (send multiple lines, verify all get responses).

**Async Job Model:**
- Test `run_prompt` returns a job ID immediately.
- Configure `FakeStore.CreateJobReturns(nil)`, `FakeEngine.GenerateReturns(result, nil)`.
- After the async goroutine runs, verify `FakeStore.CompleteJobCallCount() == 1`.
- Test job cancellation: start an async job with a slow fake, call `cancel_job`, verify `CancelJob` is called.
- Test job failure: configure engine to return an error, verify `FailJob` is called.

**Poll/Cancel/List:**
- Test `poll_result` with existing and non-existing jobs.
- Test `consume: true` calls `DeleteJob`.
- Test `cancel_job` with active and already-finished jobs.
- Test `list_jobs` with configured limit.

**Recursion Guard:**
- Test with `FakeProcessTree` that simulates a process tree with multiple claude ancestors -- verify `DetectRecursion` returns true.
- Test with a clean process tree -- verify false.
- Test with self-referential PID (loop protection).

**Metrics:**
- Test `Record` updates counters correctly.
- Test `Snapshot` returns correct averages, min, max.
- Test concurrent `Record` calls (race detector).

**HTTP Transport:**
- Use `httptest.NewServer` with `srv.ServeHTTP`.
- Test sync tool call: POST JSON-RPC, verify JSON response.
- Test async tool call: POST JSON-RPC, verify SSE stream with progress and final result.

**Spec Tool Stubs:**
- Test that each spec tool is listed in `tools/list`.
- Test that calling any spec tool returns `"not implemented yet"`.

### Integration

- Build the binary, start it, send `initialize` + `tools/list` over stdin, verify response.
- Start with `CREST_SPEC_HTTP_ADDR=:0`, verify HTTP endpoint accepts requests.

---

## Implementation Order

1. `internal/engine/engine.go` -- runner interface, Engine struct, New, acquire/release, Generate, Review
2. `internal/engine/engine.go` -- CodeReview, Bugbot (fan-out), Models/About/Status pass-through
3. `internal/engine/engine_test.go`
4. `internal/mcp/metrics.go` -- Metrics, toolMetric, Record, Snapshot
5. `internal/mcp/process.go` -- OSProcessTree, ParentProcess, SelfPID
6. `internal/mcp/recursion.go` -- DetectRecursion
7. `internal/mcp/tools.go` -- toolDef, tool registration, engine tool handlers, spec tool stubs
8. `internal/mcp/handlers.go` -- handleInitialize, handleToolsList, handleToolCall, etc.
9. `internal/mcp/server.go` -- Server struct, New, Run (stdio), ServeHTTP, runAsync, writeResponse
10. `internal/mcp/server_test.go`
11. `internal/mocks/` -- counterfeiter generation for runner, engine, store, processTree
12. `cmd/crest-spec/main.go` -- wire engine, mcp, app.Run, HTTP transport

## Key Dependencies (new in SP2)

| Package | Purpose |
|---------|---------|
| `golang.org/x/sync/errgroup` | Fan-out for CodeReview and Bugbot |

All other dependencies are already present from SP1.

## What's NOT in SP2

- CUE loader -- SP3
- Resource graph -- SP3
- Planner -- SP3
- Spec tool implementations (plan, apply, begin, next, etc.) -- SP3-SP5
- Prompt builder -- SP4
- Spec engine / constraint loop -- SP5
- MCP Resources (crest-spec://plan, etc.) -- SP3+
- MCP Prompts (system_prompt, resource_prompt, orchestrator_instructions) -- SP4+
- Fine-grained progress notifications (wave/resource tracking) -- SP5
- `bootstrap` tool (conditional registration when auth fails) -- deferred, low priority
