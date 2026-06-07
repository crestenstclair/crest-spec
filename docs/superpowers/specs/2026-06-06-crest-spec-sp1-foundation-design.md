# crest-spec Sub-project 1: Foundation

> Design specification for the foundation layer of crest-spec. This is the first of 5 sub-projects
> that incrementally build the system described in SPEC.md.

## Decomposition Overview

| Sub-project | Scope | Depends on |
|-------------|-------|------------|
| **1. Foundation** (this) | errors, config, app, migrations, sqlc, store, agent, main.go | - |
| 2. Engine + Basic MCP | engine (Generate, Review, CodeReview, Bugbot), mcp server, engine tools | SP1 |
| 3. CUE Loader + Graph + Planner | cue loader, resource graph, planner, basic spec tools | SP1, SP2 |
| 4. Prompt Builder | system prompt, resource prompt, fix prompt, context injection | SP3 |
| 5. Spec Engine + Constraint Loop | plan/apply lifecycle, wave execution, constraint loop, state machine, verify | SP1-SP4 |

## Decisions

- **Module path:** `github.com/crestenstclair/crest-spec`
- **Go version:** 1.26.3
- **Build from scratch:** no existing code to port; SPEC.md is the authoritative guide
- **All migrations upfront:** full schema defined in SP1; store API methods added incrementally per sub-project
- **Approach:** bottom-up — each package built and tested in dependency order

## Directory Layout

```
crest-spec/
├── cmd/crest-spec/main.go
├── internal/
│   ├── agent/
│   │   ├── agent.go
│   │   └── agent_test.go
│   ├── app/
│   │   └── app.go
│   ├── config/
│   │   └── config.go
│   ├── db/               # sqlc-generated (DO NOT EDIT)
│   │   ├── db.go
│   │   ├── models.go
│   │   └── queries.sql.go
│   ├── errors/
│   │   └── errors.go
│   ├── mocks/            # counterfeiter fakes (committed)
│   ├── store/
│   │   ├── store.go
│   │   └── store_test.go
├── migrations/           # SQL schema (go:embed)
│   ├── 001_jobs.sql
│   ├── 002_resources.sql
│   ├── 003_applies.sql
│   ├── 004_sessions.sql
├── sql/
│   └── queries/          # sqlc query source
│       ├── jobs.sql
│       ├── resources.sql
│       ├── applies.sql
│       ├── sessions.sql
├── sqlc.yaml
├── Makefile
├── go.mod
└── go.sum
```

## Package Designs

### 1. `internal/errors`

Const error sentinels using a string-based type.

```go
package errors

type New string

func (e New) Error() string { return string(e) }

const (
    ErrNotFound     = New("not found")
    ErrAlreadyDone  = New("already done")
    ErrLocked       = New("apply lock held")
    ErrInvalidState = New("invalid state transition")
)
```

### 2. `internal/config`

All env vars use `CREST_SPEC_` prefix via `envconfig.Process("CREST_SPEC", &cfg)`.

```go
type Config struct {
    // Engine config (adapted from claude-mcp)
    APIKey         string        `envconfig:"API_KEY"`
    AgentPath      string        `envconfig:"AGENT_PATH" default:"claude"`
    DefaultModel   string        `envconfig:"DEFAULT_MODEL" default:"claude-sonnet-4-6"`
    PermissionMode string        `envconfig:"PERMISSION_MODE" default:"default"`
    Timeout        time.Duration `envconfig:"TIMEOUT" default:"0s"`
    MaxConcurrency int           `envconfig:"MAX_CONCURRENCY" default:"5"`
    HTTPAddr       string        `envconfig:"HTTP_ADDR"`

    // Spec config
    GenerateModel   string `envconfig:"GENERATE_MODEL" default:"claude-sonnet-4-6"`
    VerifyModel     string `envconfig:"VERIFY_MODEL" default:"claude-sonnet-4-6"`
    MaxRetries      int    `envconfig:"MAX_RETRIES" default:"3"`
    WaveMaxRetries  int    `envconfig:"WAVE_MAX_RETRIES" default:"2"`
    SpecDir         string `envconfig:"SPEC_DIR" default:"./spec"`
    TypeCheckCommand string `envconfig:"TYPE_CHECK_CMD"`
    TestCommand      string `envconfig:"TEST_CMD"`
}

func New() (*Config, error)    // envconfig.Process
func Help()                    // tabwriter + envconfig.Usagef to stderr
```

### 3. `internal/app`

Minimal lifecycle wrapper.

```go
type server interface {
    Run(ctx context.Context) error
}

type App struct{ s server }

func New(s server) *App
func (a *App) Run(ctx context.Context) error
```

### 4. SQLite Schema (All Migrations)

All tables defined upfront. Embedded via `go:embed` in the store package. Applied transactionally in filename order, tracked in `schema_migrations(filename)`.

#### `001_jobs.sql`

```sql
CREATE TABLE jobs (
    id         TEXT PRIMARY KEY,
    tool       TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'running'
               CHECK (status IN ('running','completed','failed','cancelled','deleted')),
    result     TEXT,
    error      TEXT,
    pid        INTEGER NOT NULL,
    started_at TEXT NOT NULL,
    done_at    TEXT
);
CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_pid ON jobs(pid);
```

#### `002_resources.sql`

```sql
CREATE TABLE resources (
    id               TEXT PRIMARY KEY,
    kind             TEXT NOT NULL,
    context_name     TEXT,
    declaration_hash TEXT NOT NULL,
    effective_hash   TEXT NOT NULL,
    model            TEXT,
    settled_at       TEXT NOT NULL
);

CREATE TABLE generated_files (
    path         TEXT PRIMARY KEY,
    resource_id  TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    content_hash TEXT NOT NULL,
    prompt_hash  TEXT NOT NULL,
    model        TEXT NOT NULL,
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_generated_files_resource ON generated_files(resource_id);

CREATE TABLE dependencies (
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    kind      TEXT NOT NULL,
    PRIMARY KEY (source_id, target_id, kind)
);
```

#### `003_applies.sql`

```sql
CREATE TABLE applies (
    id        TEXT PRIMARY KEY,
    status    TEXT NOT NULL DEFAULT 'running'
              CHECK (status IN ('running','completed','failed','cancelled')),
    spec_hash TEXT NOT NULL,
    started_at TEXT NOT NULL,
    done_at    TEXT
);

CREATE TABLE apply_actions (
    id          TEXT PRIMARY KEY,
    apply_id    TEXT NOT NULL REFERENCES applies(id),
    resource_id TEXT NOT NULL,
    action      TEXT NOT NULL CHECK (action IN ('create','modify','destroy')),
    outcome     TEXT CHECK (outcome IN ('committed','rejected','skipped','errored')),
    error       TEXT,
    started_at  TEXT NOT NULL,
    done_at     TEXT
);
CREATE INDEX idx_apply_actions_apply ON apply_actions(apply_id);

CREATE TABLE generations (
    id               TEXT PRIMARY KEY,
    apply_id         TEXT REFERENCES applies(id),
    resource_id      TEXT NOT NULL,
    prompt_text      TEXT NOT NULL,
    prompt_hash      TEXT NOT NULL,
    output_text      TEXT,
    model            TEXT NOT NULL,
    outcome          TEXT CHECK (outcome IN ('accepted','rejected')),
    rejection_reason TEXT,
    retry_count      INTEGER NOT NULL DEFAULT 0,
    duration_ms      INTEGER,
    input_tokens     INTEGER,
    output_tokens    INTEGER,
    cost_usd         REAL,
    created_at       TEXT NOT NULL
);
CREATE INDEX idx_generations_resource ON generations(resource_id);
CREATE INDEX idx_generations_apply ON generations(apply_id);

CREATE TABLE invariant_checks (
    id          TEXT PRIMARY KEY,
    apply_id    TEXT NOT NULL REFERENCES applies(id),
    resource_id TEXT NOT NULL,
    invariant   TEXT NOT NULL,
    passed      INTEGER NOT NULL,
    details     TEXT,
    checked_at  TEXT NOT NULL
);
```

#### `004_sessions.sql`

```sql
CREATE TABLE agent_sessions (
    id           TEXT PRIMARY KEY,
    plan_json    TEXT NOT NULL,
    waves_json   TEXT NOT NULL,
    hashes_json  TEXT NOT NULL,
    current_wave INTEGER NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active','completed','aborted')),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE TABLE agent_notes (
    resource_id TEXT NOT NULL,
    apply_id    TEXT NOT NULL,
    content     TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (resource_id, apply_id)
);

CREATE TABLE lock (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    holder      TEXT NOT NULL,
    pid         INTEGER NOT NULL,
    acquired_at TEXT NOT NULL
);
```

### 5. Store API

Single `Store` struct. Constructor opens SQLite with WAL + busy_timeout=5000, runs migrations.

**Migration runner:** The store creates `schema_migrations(filename TEXT PRIMARY KEY)` if it doesn't exist, then applies each embedded SQL file in filename order. Each migration is applied transactionally. Already-applied files (present in `schema_migrations`) are skipped.

**Note on `internal/jobs/`:** SPEC.md lists a separate `jobs/` package. The raw CRUD operations (create, complete, fail, cancel, get, list, delete, cleanup, wait) live on the Store because they're SQLite operations. The async goroutine lifecycle (spawn, cancel maps, `asyncWg`, progress callbacks) lives in the MCP server layer (SP2) which orchestrates job state transitions through the store.

#### Fully implemented in SP1 (needed for SP2 engine/MCP)

**Job operations:**
- `CreateJob(id, tool string, pid int) error`
- `CompleteJob(id, result string) error` — `AND status = 'running'` guard
- `FailJob(id string, jobErr error) error` — `AND status = 'running'` guard
- `CancelJob(id string) error` — `AND status = 'running'` guard
- `GetJob(id string) (*Job, error)`
- `ListJobs(limit int) ([]Job, error)` — ordered by `started_at DESC`, excludes deleted
- `DeleteJob(id string) error` — soft-delete to `'deleted'`
- `CleanupOrphans(aliveFn func(int) bool) (int, error)` — finds running jobs, checks PIDs, marks dead ones failed
- `WaitForCompletion(ctx context.Context, id string) (*Job, error)` — exponential backoff: 100ms initial, 2x, cap 2s

**Lock operations:**
- `AcquireLock(holder string, pid int) error` — INSERT OR fail if row exists
- `ReleaseLock() error` — DELETE WHERE id=1
- `GetLock() (*Lock, error)`

#### Stub signatures in SP1 (implemented in later sub-projects)

**Resource state:** GetResource, SetResource, ListResources, DeleteResource, SetGeneratedFile, GetGeneratedFiles, SetDependency

**Apply/audit:** CreateApply, CompleteApply, RecordAction, RecordGeneration, RecordInvariantCheck

**Session:** CreateSession, GetSession, UpdateSession, GetNote, SetNote, ListNotes

**Lifecycle:** `Close()` shuts down DB connection.

### 6. Agent Wrapper

`internal/agent/Agent` wraps the `claude` CLI.

#### Types

```go
type RunOpts struct {
    Prompt              string
    Model               string
    Mode                string
    Effort              string   // "low" | "medium" | "high" | "max"
    Cwd                 string
    RelevantPaths       []string
    AddDirs             []string
    Continue            bool
    Resume              bool
    SessionID           string
    Force               bool
    AllowedTools        []string
    DisallowedTools     []string
    AppendSystemPrompt  string
    NoSessionPersistence bool
}

type RunResult struct {
    Output    string
    Stderr    string
    Model     string
    SessionID string
    DurationMS int64
    NumTurns   int
    CostUSD    float64
    IsError    bool
    Usage      *Usage
}

type Usage struct {
    InputTokens          int
    OutputTokens         int
    CacheReadTokens      int
    CacheCreationTokens  int
}
```

#### Constructor

`New(path, apiKey, defaultModel, permissionMode string, timeout time.Duration) *Agent`

#### `RunPrompt(ctx context.Context, opts RunOpts) (*RunResult, error)`

1. Build argv: always `--print --output-format json`
2. Map RunOpts fields to CLI flags
3. Prompt routing: `len(prompt) <= 8192` → positional arg; else → stdin pipe
4. **Config isolation:**
   - `os.MkdirTemp("", "crest-spec-claude-*")`
   - Copy `~/.claude/.claude.json` (writable per-process)
   - Hard-link credential files
   - Symlink subdirs
   - Set `CLAUDE_CONFIG_DIR=<temp>` in subprocess env
   - Defer `os.RemoveAll(temp)`
5. **Process groups:**
   - `SysProcAttr{Setpgid: true}`
   - `cmd.Cancel = func() { syscall.Kill(-pid, SIGKILL) }`
   - `cmd.WaitDelay = 5 * time.Second`
6. **Error handling:**
   - On any error: parse partial stdout into `RunResult`, attach Stderr, return `(partial, wrapped)`
   - On success: parse JSON envelope, check `is_error` field → if true, surface as error

#### Read-only Commands

- `Models(ctx context.Context) (string, error)` — `claude models`
- `About(ctx context.Context) (string, error)` — `claude --version`
- `Status(ctx context.Context) (string, error)` — `claude auth status`

### 7. Main.go Entrypoint

Follows SPEC.md section 2.4 startup sequence:

1. **Subcommand check** — if `os.Args` matches `check job <id>`, run `checkJob()` and `os.Exit`
2. **Help** — `-h`/`--help` → `config.Help()`, `os.Exit(0)`
3. **Config** — `config.New()`; on error, print help, panic
4. **Store** — `store.New(dbPath())` where `dbPath()` = `.crest-spec/state.db` relative to cwd; `defer store.Close()`
5. **Orphan cleanup** — `store.CleanupOrphans(processAlive)` — logged, non-fatal on error
6. **Signal context** — `signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)`
7. **Agent** — `agent.New(...)` from config fields

Steps 8-10 (Engine, Spec, MCP) are stubs until later sub-projects. For now, main.go logs readiness and blocks on `<-ctx.Done()`.

#### `checkJob(id string)` subcommand

1. Open store at `.crest-spec/state.db`, defer Close
2. `CleanupOrphans(processAlive)`
3. Signal context for SIGINT/SIGTERM
4. `WaitForCompletion(ctx, id)` — blocks with exponential backoff
5. `completed` → print result to stdout, `DeleteJob`, exit 0
6. `failed`/`cancelled` → print status+error to stderr, `DeleteJob`, exit 1

#### `processAlive(pid int) bool`

`syscall.Kill(pid, 0)` — returns true if signal delivery succeeds (process exists), false on error.

### 8. Mocks

counterfeiter fakes committed under `internal/mocks/`. Generated via `//go:generate` directives.

For SP1, the key interface to mock is the `runner` interface (used by the engine in SP2):

```go
type runner interface {
    RunPrompt(ctx context.Context, opts agent.RunOpts) (*agent.RunResult, error)
    Models(ctx context.Context) (string, error)
    About(ctx context.Context) (string, error)
    Status(ctx context.Context) (string, error)
}
```

This interface is defined in the `engine` package (SP2), but the mock will be generated from the `agent.Agent` methods. For SP1, we generate a `FakeAgent` that implements the full Agent interface for testing.

### 9. Build Tooling

**Makefile:**
- `make` / `make all` → `fmt test lint`
- `make build` → `go build -o bin/crest-spec ./cmd/crest-spec`
- `make install` → `go install ./cmd/crest-spec`
- `make test` → `go test ./...`
- `make fmt` → `go fmt ./...` + goimports-reviser (if available)
- `make mocks` → `go generate ./internal/mocks/...`
- `make sqlc` → `sqlc generate`
- `make lint` → `golangci-lint run`
- `make lint-fix` → `golangci-lint run --fix`
- `make update` → `go get -u ./... && go mod tidy`

**sqlc.yaml:** configured for `modernc.org/sqlite`, pointing at `migrations/` for schema and `sql/queries/` for query sources, outputting to `internal/db/`.

## Testing Strategy

- **Store:** table-driven tests against real SQLite (`:memory:` or temp file). Cover all state transitions, guard clauses (`AND status = 'running'`), orphan detection, exponential backoff timing, lock contention. Error cases first, success last per convention.
- **Agent:** tests use a fake `claude` binary (shell script echoing JSON). Test config isolation (temp dir creation/cleanup). Test process group kill behavior. Test prompt size routing (positional vs stdin).
- **Config:** test env var parsing with `t.Setenv()`. Test Help() output.
- **Errors:** trivial — test Error() returns the string.
- **Integration:** launch binary with `--help` (exit 0, output check). Launch `check job` with pre-seeded DB.

## Implementation Order

1. `go.mod` + `go.sum` (init module, add dependencies)
2. `internal/errors/errors.go`
3. `internal/config/config.go`
4. `internal/app/app.go`
5. `migrations/*.sql` (all 4 migration files)
6. `sqlc.yaml` + `sql/queries/*.sql` → `make sqlc` → `internal/db/`
7. `internal/store/store.go` + tests (job ops, lock ops, migration runner)
8. `internal/agent/agent.go` + tests
9. `cmd/crest-spec/main.go` (startup + checkJob)
10. `Makefile`
11. `internal/mocks/` (counterfeiter generation)

## Key Dependencies

| Package | Purpose |
|---------|---------|
| `modernc.org/sqlite` | CGO-free SQLite driver |
| `github.com/google/uuid` | UUID generation for job/apply IDs |
| `github.com/kelseyhightower/envconfig` | Config from env vars |
| `github.com/rs/zerolog` | Structured logging |
| `github.com/stretchr/testify` | Test assertions |

## What's NOT in SP1

- Engine (Generate, Review, CodeReview, Bugbot) — SP2
- MCP server (JSON-RPC, dispatch, transports) — SP2
- CUE loader — SP3
- Resource graph — SP3
- Planner — SP3
- Prompt builder — SP4
- Spec engine / constraint loop — SP5
- Verification — SP5
