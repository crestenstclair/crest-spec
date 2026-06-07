# crest-spec Sub-project 1: Foundation — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the foundation layer for crest-spec — error types, config, app lifecycle, SQLite store with all migrations and job/lock operations, and the claude CLI agent wrapper.

**Architecture:** Bottom-up build in dependency order. Each package is fully tested before the next depends on it. SQLite via modernc.org/sqlite (CGO-free). The store owns all database operations; the agent wraps the `claude` CLI with config isolation and process groups. A minimal main.go wires everything together.

**Tech Stack:** Go 1.26.3, modernc.org/sqlite, envconfig, zerolog, testify, sqlc, counterfeiter

**Prerequisites:** Go 1.26.3+ and sqlc must be installed. If not available:
```bash
# Go — see https://go.dev/dl/
# sqlc
go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest
```

**Spec:** `docs/superpowers/specs/2026-06-06-crest-spec-sp1-foundation-design.md`

---

## File Map

| File | Responsibility |
|------|---------------|
| `go.mod` | Module definition and dependencies |
| `internal/errors/errors.go` | Const error sentinels (`type New string`) |
| `internal/config/config.go` | envconfig struct with `CREST_SPEC_` prefix, `New()`, `Help()` |
| `internal/config/config_test.go` | Env var parsing and default tests |
| `internal/app/app.go` | Minimal `New(server) + Run(ctx)` lifecycle wrapper |
| `migrations/001_jobs.sql` | Jobs table schema |
| `migrations/002_resources.sql` | Resources, generated_files, dependencies tables |
| `migrations/003_applies.sql` | Applies, apply_actions, generations, invariant_checks tables |
| `migrations/004_sessions.sql` | Agent_sessions, agent_notes, lock tables |
| `sqlc.yaml` | sqlc configuration for SQLite |
| `sql/queries/jobs.sql` | sqlc query definitions for jobs table |
| `sql/queries/lock.sql` | sqlc query definitions for lock table |
| `internal/db/` | sqlc-generated code (DO NOT EDIT) |
| `internal/store/store.go` | Store struct, constructor, migration runner, job ops, lock ops |
| `internal/store/store_test.go` | Table-driven tests against real SQLite |
| `internal/agent/agent.go` | Agent struct, types, RunPrompt, config isolation, read-only commands |
| `internal/agent/agent_test.go` | Tests with fake claude binary |
| `cmd/crest-spec/main.go` | Entrypoint: startup sequence, checkJob subcommand |
| `Makefile` | Build, test, lint, sqlc, fmt targets |

---

### Task 1: Project Scaffolding

**Files:**
- Create: `go.mod`

- [ ] **Step 1: Initialize Go module and add dependencies**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server
go mod init github.com/crestenstclair/crest-spec
```

- [ ] **Step 2: Create directory structure**

```bash
mkdir -p cmd/crest-spec
mkdir -p internal/{agent,app,config,db,errors,mocks,store}
mkdir -p migrations
mkdir -p sql/queries
```

- [ ] **Step 3: Add dependencies**

```bash
go get modernc.org/sqlite
go get github.com/google/uuid
go get github.com/kelseyhightower/envconfig
go get github.com/rs/zerolog
go get github.com/stretchr/testify
```

- [ ] **Step 4: Verify go.mod**

Run: `cat go.mod`
Expected: Module path is `github.com/crestenstclair/crest-spec`, Go version is present, all 5 dependencies listed.

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum
git commit -m "feat: initialize Go module with dependencies"
```

---

### Task 2: Errors Package

**Files:**
- Create: `internal/errors/errors.go`

- [ ] **Step 1: Write the errors package**

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

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/errors/`
Expected: No output (success).

- [ ] **Step 3: Commit**

```bash
git add internal/errors/errors.go
git commit -m "feat: add const error sentinel type"
```

---

### Task 3: Config Package

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing test**

```go
package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_Defaults(t *testing.T) {
	cfg, err := New()
	require.NoError(t, err)

	assert.Equal(t, "claude", cfg.AgentPath)
	assert.Equal(t, "claude-sonnet-4-6", cfg.DefaultModel)
	assert.Equal(t, "default", cfg.PermissionMode)
	assert.Equal(t, 5, cfg.MaxConcurrency)
	assert.Equal(t, "claude-sonnet-4-6", cfg.GenerateModel)
	assert.Equal(t, "claude-sonnet-4-6", cfg.VerifyModel)
	assert.Equal(t, 3, cfg.MaxRetries)
	assert.Equal(t, 2, cfg.WaveMaxRetries)
	assert.Equal(t, "./spec", cfg.SpecDir)
	assert.Empty(t, cfg.APIKey)
	assert.Empty(t, cfg.HTTPAddr)
	assert.Empty(t, cfg.TypeCheckCommand)
	assert.Empty(t, cfg.TestCommand)
}

func TestNew_EnvOverrides(t *testing.T) {
	t.Setenv("CREST_SPEC_AGENT_PATH", "/usr/local/bin/claude")
	t.Setenv("CREST_SPEC_DEFAULT_MODEL", "claude-opus-4-8")
	t.Setenv("CREST_SPEC_MAX_CONCURRENCY", "10")
	t.Setenv("CREST_SPEC_MAX_RETRIES", "5")
	t.Setenv("CREST_SPEC_SPEC_DIR", "/tmp/specs")

	cfg, err := New()
	require.NoError(t, err)

	assert.Equal(t, "/usr/local/bin/claude", cfg.AgentPath)
	assert.Equal(t, "claude-opus-4-8", cfg.DefaultModel)
	assert.Equal(t, 10, cfg.MaxConcurrency)
	assert.Equal(t, 5, cfg.MaxRetries)
	assert.Equal(t, "/tmp/specs", cfg.SpecDir)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `New` not defined.

- [ ] **Step 3: Write the implementation**

```go
package config

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	APIKey         string        `envconfig:"API_KEY"`
	AgentPath      string        `envconfig:"AGENT_PATH" default:"claude"`
	DefaultModel   string        `envconfig:"DEFAULT_MODEL" default:"claude-sonnet-4-6"`
	PermissionMode string        `envconfig:"PERMISSION_MODE" default:"default"`
	Timeout        time.Duration `envconfig:"TIMEOUT" default:"0s"`
	MaxConcurrency int           `envconfig:"MAX_CONCURRENCY" default:"5"`
	HTTPAddr       string        `envconfig:"HTTP_ADDR"`

	GenerateModel    string `envconfig:"GENERATE_MODEL" default:"claude-sonnet-4-6"`
	VerifyModel      string `envconfig:"VERIFY_MODEL" default:"claude-sonnet-4-6"`
	MaxRetries       int    `envconfig:"MAX_RETRIES" default:"3"`
	WaveMaxRetries   int    `envconfig:"WAVE_MAX_RETRIES" default:"2"`
	SpecDir          string `envconfig:"SPEC_DIR" default:"./spec"`
	TypeCheckCommand string `envconfig:"TYPE_CHECK_CMD"`
	TestCommand      string `envconfig:"TEST_CMD"`
}

func New() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("CREST_SPEC", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func Help() {
	w := tabwriter.NewWriter(os.Stderr, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "crest-spec — declarative code generation MCP server")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Environment variables:")
	fmt.Fprintln(w)
	_ = envconfig.Usagef("CREST_SPEC", &Config{}, w, envconfig.DefaultTableFormat)
	w.Flush()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS — both tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add envconfig-based configuration"
```

---

### Task 4: App Lifecycle Package

**Files:**
- Create: `internal/app/app.go`

- [ ] **Step 1: Write the app package**

```go
package app

import "context"

type server interface {
	Run(ctx context.Context) error
}

type App struct {
	s server
}

func New(s server) *App {
	return &App{s: s}
}

func (a *App) Run(ctx context.Context) error {
	return a.s.Run(ctx)
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/app/`
Expected: No output (success).

- [ ] **Step 3: Commit**

```bash
git add internal/app/app.go
git commit -m "feat: add app lifecycle wrapper"
```

---

### Task 5: SQL Migrations

**Files:**
- Create: `migrations/001_jobs.sql`
- Create: `migrations/002_resources.sql`
- Create: `migrations/003_applies.sql`
- Create: `migrations/004_sessions.sql`

- [ ] **Step 1: Write 001_jobs.sql**

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

- [ ] **Step 2: Write 002_resources.sql**

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

- [ ] **Step 3: Write 003_applies.sql**

```sql
CREATE TABLE applies (
    id         TEXT PRIMARY KEY,
    status     TEXT NOT NULL DEFAULT 'running'
               CHECK (status IN ('running','completed','failed','cancelled')),
    spec_hash  TEXT NOT NULL,
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

- [ ] **Step 4: Write 004_sessions.sql**

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

- [ ] **Step 5: Commit**

```bash
git add migrations/
git commit -m "feat: add all SQLite migration files"
```

---

### Task 6: sqlc Setup and Code Generation

**Files:**
- Create: `sqlc.yaml`
- Create: `sql/queries/jobs.sql`
- Create: `sql/queries/lock.sql`
- Generated: `internal/db/db.go`, `internal/db/models.go`, `internal/db/query.sql.go`

- [ ] **Step 1: Write sqlc.yaml**

```yaml
version: "2"
sql:
  - engine: "sqlite"
    queries: "sql/queries"
    schema: "migrations"
    gen:
      go:
        package: "db"
        out: "internal/db"
        emit_pointers_for_null_types: true
```

- [ ] **Step 2: Write sql/queries/jobs.sql**

```sql
-- name: CreateJob :exec
INSERT INTO jobs (id, tool, status, pid, started_at)
VALUES (?, ?, 'running', ?, ?);

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- name: CompleteJob :execresult
UPDATE jobs SET status = 'completed', result = ?, done_at = ?
WHERE id = ? AND status = 'running';

-- name: FailJob :execresult
UPDATE jobs SET status = 'failed', error = ?, done_at = ?
WHERE id = ? AND status = 'running';

-- name: CancelJob :execresult
UPDATE jobs SET status = 'cancelled', done_at = ?
WHERE id = ? AND status = 'running';

-- name: DeleteJob :exec
UPDATE jobs SET status = 'deleted', done_at = ?
WHERE id = ?;

-- name: ListJobs :many
SELECT * FROM jobs
WHERE status != 'deleted'
ORDER BY started_at DESC
LIMIT ?;

-- name: ListRunningJobs :many
SELECT * FROM jobs WHERE status = 'running';
```

- [ ] **Step 3: Write sql/queries/lock.sql**

```sql
-- name: InsertLock :exec
INSERT INTO lock (id, holder, pid, acquired_at)
VALUES (1, ?, ?, ?);

-- name: DeleteLock :exec
DELETE FROM lock WHERE id = 1;

-- name: GetLock :one
SELECT * FROM lock WHERE id = 1;
```

- [ ] **Step 4: Run sqlc generate**

Run: `sqlc generate`
Expected: No errors. Files created in `internal/db/`: `db.go`, `models.go`, `jobs.sql.go`, `lock.sql.go`.

- [ ] **Step 5: Verify generated code compiles**

Run: `go build ./internal/db/`
Expected: No output (success).

- [ ] **Step 6: Commit**

```bash
git add sqlc.yaml sql/queries/ internal/db/
git commit -m "feat: add sqlc config and generated query code"
```

---

### Task 7: Store — Constructor and Migration Runner

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test for store construction and migrations**

```go
package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNew_AppliesMigrations(t *testing.T) {
	s := testStore(t)

	// Verify the jobs table exists by querying it
	var count int
	err := s.sqlDB.QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestNew_AllTablesExist(t *testing.T) {
	s := testStore(t)

	tables := []string{
		"jobs", "resources", "generated_files", "dependencies",
		"applies", "apply_actions", "generations", "invariant_checks",
		"agent_sessions", "agent_notes", "lock",
		"schema_migrations",
	}
	for _, table := range tables {
		var name string
		err := s.sqlDB.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		require.NoError(t, err, "table %s should exist", table)
	}
}

func TestNew_MigrationsIdempotent(t *testing.T) {
	s := testStore(t)

	// Running migrations again should not error
	err := s.migrate()
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -v -run TestNew`
Expected: FAIL — `New`, `Store` not defined.

- [ ] **Step 3: Write the store constructor and migration runner**

```go
package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/crestenstclair/crest-spec/internal/db"
	"github.com/crestenstclair/crest-spec/migrations"
	_ "modernc.org/sqlite"
)

type Job struct {
	ID        string
	Tool      string
	Status    string
	Result    string
	Error     string
	PID       int
	StartedAt time.Time
	DoneAt    *time.Time
}

type Lock struct {
	Holder     string
	PID        int
	AcquiredAt time.Time
}

type Store struct {
	sqlDB   *sql.DB
	queries *db.Queries
}

func New(dbPath string) (*Store, error) {
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	if _, err := sqlDB.Exec("PRAGMA journal_mode=WAL"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA busy_timeout=5000"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}
	if _, err := sqlDB.Exec("PRAGMA foreign_keys=ON"); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	s := &Store{
		sqlDB:   sqlDB,
		queries: db.New(sqlDB),
	}

	if err := s.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.sqlDB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		filename TEXT PRIMARY KEY
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		var applied string
		err := s.sqlDB.QueryRow(
			"SELECT filename FROM schema_migrations WHERE filename = ?",
			entry.Name(),
		).Scan(&applied)
		if err == nil {
			continue
		}

		content, err := fs.ReadFile(migrations.FS, entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}

		tx, err := s.sqlDB.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for %s: %w", entry.Name(), err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("execute migration %s: %w", entry.Name(), err)
		}

		if _, err := tx.Exec(
			"INSERT INTO schema_migrations (filename) VALUES (?)",
			entry.Name(),
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
	}

	return nil
}

func (s *Store) Close() error {
	return s.sqlDB.Close()
}
```

- [ ] **Step 4: Create migrations/migrations.go**

```go
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/ -v -run TestNew`
Expected: PASS — all three tests pass.

- [ ] **Step 6: Commit**

```bash
git add migrations/migrations.go internal/store/store.go internal/store/store_test.go
git commit -m "feat: add store constructor with migration runner"
```

---

### Task 8: Store — Job CRUD Operations

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

- [ ] **Step 1: Write failing tests for job CRUD**

Add to `store_test.go`:

```go
func TestCreateJob_And_GetJob(t *testing.T) {
	s := testStore(t)

	err := s.CreateJob("job-1", "run_prompt", 1234)
	require.NoError(t, err)

	job, err := s.GetJob("job-1")
	require.NoError(t, err)
	assert.Equal(t, "job-1", job.ID)
	assert.Equal(t, "run_prompt", job.Tool)
	assert.Equal(t, "running", job.Status)
	assert.Equal(t, 1234, job.PID)
	assert.Empty(t, job.Result)
	assert.Empty(t, job.Error)
	assert.Nil(t, job.DoneAt)
	assert.False(t, job.StartedAt.IsZero())
}

func TestGetJob_NotFound(t *testing.T) {
	s := testStore(t)

	_, err := s.GetJob("nonexistent")
	require.ErrorIs(t, err, cserrors.ErrNotFound)
}

func TestCompleteJob(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("job-1", "run_prompt", 1234))

	err := s.CompleteJob("job-1", `{"output":"hello"}`)
	require.NoError(t, err)

	job, err := s.GetJob("job-1")
	require.NoError(t, err)
	assert.Equal(t, "completed", job.Status)
	assert.Equal(t, `{"output":"hello"}`, job.Result)
	assert.NotNil(t, job.DoneAt)
}

func TestCompleteJob_AlreadyDone(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("job-1", "run_prompt", 1234))
	require.NoError(t, s.CompleteJob("job-1", "result"))

	err := s.CompleteJob("job-1", "other")
	require.ErrorIs(t, err, cserrors.ErrAlreadyDone)
}

func TestFailJob(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("job-1", "run_prompt", 1234))

	err := s.FailJob("job-1", fmt.Errorf("something broke"))
	require.NoError(t, err)

	job, err := s.GetJob("job-1")
	require.NoError(t, err)
	assert.Equal(t, "failed", job.Status)
	assert.Equal(t, "something broke", job.Error)
	assert.NotNil(t, job.DoneAt)
}

func TestFailJob_AlreadyDone(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("job-1", "run_prompt", 1234))
	require.NoError(t, s.FailJob("job-1", fmt.Errorf("err")))

	err := s.FailJob("job-1", fmt.Errorf("again"))
	require.ErrorIs(t, err, cserrors.ErrAlreadyDone)
}

func TestCancelJob(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("job-1", "run_prompt", 1234))

	err := s.CancelJob("job-1")
	require.NoError(t, err)

	job, err := s.GetJob("job-1")
	require.NoError(t, err)
	assert.Equal(t, "cancelled", job.Status)
	assert.NotNil(t, job.DoneAt)
}

func TestDeleteJob(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("job-1", "run_prompt", 1234))

	err := s.DeleteJob("job-1")
	require.NoError(t, err)

	job, err := s.GetJob("job-1")
	require.NoError(t, err)
	assert.Equal(t, "deleted", job.Status)
}

func TestListJobs(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("job-1", "run_prompt", 1234))
	require.NoError(t, s.CreateJob("job-2", "code_review", 1234))
	require.NoError(t, s.CreateJob("job-3", "bugbot", 1234))
	require.NoError(t, s.DeleteJob("job-3"))

	jobs, err := s.ListJobs(50)
	require.NoError(t, err)
	assert.Len(t, jobs, 2)
}

func TestListJobs_RespectsLimit(t *testing.T) {
	s := testStore(t)
	for i := 0; i < 5; i++ {
		require.NoError(t, s.CreateJob(fmt.Sprintf("job-%d", i), "run_prompt", 1234))
	}

	jobs, err := s.ListJobs(3)
	require.NoError(t, err)
	assert.Len(t, jobs, 3)
}
```

Add these imports at the top of `store_test.go`:

```go
import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
)
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -v -run "TestCreate|TestGet|TestComplete|TestFail|TestCancel|TestDelete|TestList"`
Expected: FAIL — methods not defined.

- [ ] **Step 3: Implement job CRUD methods**

Add to `store.go`:

```go
import (
	"context"
	"database/sql"
	"time"

	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
)

func (s *Store) CreateJob(id, tool string, pid int) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return s.queries.CreateJob(context.Background(), db.CreateJobParams{
		ID:        id,
		Tool:      tool,
		Pid:       int64(pid),
		StartedAt: now,
	})
}

func (s *Store) GetJob(id string) (*Job, error) {
	row, err := s.queries.GetJob(context.Background(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, cserrors.ErrNotFound
		}
		return nil, err
	}
	return dbJobToJob(row), nil
}

func (s *Store) CompleteJob(id, result string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.queries.CompleteJob(context.Background(), db.CompleteJobParams{
		Result: &result,
		DoneAt: &now,
		ID:     id,
	})
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return cserrors.ErrAlreadyDone
	}
	return nil
}

func (s *Store) FailJob(id string, jobErr error) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	errStr := jobErr.Error()
	res, err := s.queries.FailJob(context.Background(), db.FailJobParams{
		Error:  &errStr,
		DoneAt: &now,
		ID:     id,
	})
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return cserrors.ErrAlreadyDone
	}
	return nil
}

func (s *Store) CancelJob(id string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.queries.CancelJob(context.Background(), db.CancelJobParams{
		DoneAt: &now,
		ID:     id,
	})
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return cserrors.ErrAlreadyDone
	}
	return nil
}

func (s *Store) DeleteJob(id string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return s.queries.DeleteJob(context.Background(), db.DeleteJobParams{
		DoneAt: &now,
		ID:     id,
	})
}

func (s *Store) ListJobs(limit int) ([]Job, error) {
	rows, err := s.queries.ListJobs(context.Background(), int64(limit))
	if err != nil {
		return nil, err
	}
	jobs := make([]Job, len(rows))
	for i, row := range rows {
		jobs[i] = *dbJobToJob(row)
	}
	return jobs, nil
}

func dbJobToJob(row db.Job) *Job {
	j := &Job{
		ID:     row.ID,
		Tool:   row.Tool,
		Status: row.Status,
		PID:    int(row.Pid),
	}

	t, err := time.Parse(time.RFC3339Nano, row.StartedAt)
	if err == nil {
		j.StartedAt = t
	}

	if row.Result != nil {
		j.Result = *row.Result
	}
	if row.Error != nil {
		j.Error = *row.Error
	}
	if row.DoneAt != nil {
		t, err := time.Parse(time.RFC3339Nano, *row.DoneAt)
		if err == nil {
			j.DoneAt = &t
		}
	}

	return j
}
```

Note: The exact field names in sqlc-generated params structs depend on the sqlc output. After running `sqlc generate` in Task 6, check `internal/db/jobs.sql.go` and adjust the field names in the store methods if they differ from what's shown here. sqlc derives names from the SQL column names (e.g., `done_at` → `DoneAt`, `result` → `Result`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -v -run "TestCreate|TestGet|TestComplete|TestFail|TestCancel|TestDelete|TestList"`
Expected: PASS — all tests pass. If any field names from sqlc don't match, fix them based on the generated code.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat: add store job CRUD operations"
```

---

### Task 9: Store — CleanupOrphans and WaitForCompletion

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

- [ ] **Step 1: Write failing tests**

Add to `store_test.go`:

```go
func TestCleanupOrphans(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("job-alive", "run_prompt", 1000))
	require.NoError(t, s.CreateJob("job-dead", "run_prompt", 9999))

	aliveCheck := func(pid int) bool {
		return pid == 1000
	}

	cleaned, err := s.CleanupOrphans(aliveCheck)
	require.NoError(t, err)
	assert.Equal(t, 1, cleaned)

	alive, err := s.GetJob("job-alive")
	require.NoError(t, err)
	assert.Equal(t, "running", alive.Status)

	dead, err := s.GetJob("job-dead")
	require.NoError(t, err)
	assert.Equal(t, "failed", dead.Status)
	assert.Contains(t, dead.Error, "orphan")
}

func TestCleanupOrphans_NoRunningJobs(t *testing.T) {
	s := testStore(t)

	cleaned, err := s.CleanupOrphans(func(int) bool { return true })
	require.NoError(t, err)
	assert.Equal(t, 0, cleaned)
}

func TestWaitForCompletion_AlreadyDone(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("job-1", "run_prompt", 1234))
	require.NoError(t, s.CompleteJob("job-1", "done"))

	ctx := context.Background()
	job, err := s.WaitForCompletion(ctx, "job-1")
	require.NoError(t, err)
	assert.Equal(t, "completed", job.Status)
}

func TestWaitForCompletion_WaitsForCompletion(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("job-1", "run_prompt", 1234))

	go func() {
		time.Sleep(200 * time.Millisecond)
		s.CompleteJob("job-1", "result-data")
	}()

	ctx := context.Background()
	job, err := s.WaitForCompletion(ctx, "job-1")
	require.NoError(t, err)
	assert.Equal(t, "completed", job.Status)
	assert.Equal(t, "result-data", job.Result)
}

func TestWaitForCompletion_RespectsContext(t *testing.T) {
	s := testStore(t)
	require.NoError(t, s.CreateJob("job-1", "run_prompt", 1234))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := s.WaitForCompletion(ctx, "job-1")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWaitForCompletion_NotFound(t *testing.T) {
	s := testStore(t)

	ctx := context.Background()
	_, err := s.WaitForCompletion(ctx, "nonexistent")
	require.ErrorIs(t, err, cserrors.ErrNotFound)
}
```

Add `"context"` and `"time"` to the imports in `store_test.go`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -v -run "TestCleanup|TestWait"`
Expected: FAIL — `CleanupOrphans`, `WaitForCompletion` not defined.

- [ ] **Step 3: Implement CleanupOrphans and WaitForCompletion**

Add to `store.go`:

```go
func (s *Store) CleanupOrphans(aliveFn func(int) bool) (int, error) {
	rows, err := s.queries.ListRunningJobs(context.Background())
	if err != nil {
		return 0, err
	}

	pids := make(map[int64]bool)
	for _, row := range rows {
		if _, checked := pids[row.Pid]; !checked {
			pids[row.Pid] = aliveFn(int(row.Pid))
		}
	}

	cleaned := 0
	for _, row := range rows {
		if !pids[row.Pid] {
			now := time.Now().UTC().Format(time.RFC3339Nano)
			errStr := fmt.Sprintf("orphan: owner process %d is dead", row.Pid)
			s.queries.FailJob(context.Background(), db.FailJobParams{
				Error:  &errStr,
				DoneAt: &now,
				ID:     row.ID,
			})
			cleaned++
		}
	}

	return cleaned, nil
}

func (s *Store) WaitForCompletion(ctx context.Context, id string) (*Job, error) {
	delay := 100 * time.Millisecond
	maxDelay := 2 * time.Second

	for {
		job, err := s.GetJob(id)
		if err != nil {
			return nil, err
		}
		if job.Status != "running" {
			return job, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}

		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -v -run "TestCleanup|TestWait"`
Expected: PASS — all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat: add store orphan cleanup and wait-for-completion"
```

---

### Task 10: Store — Lock Operations

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

- [ ] **Step 1: Write failing tests**

Add to `store_test.go`:

```go
func TestAcquireLock(t *testing.T) {
	s := testStore(t)

	err := s.AcquireLock("test-session", 1234)
	require.NoError(t, err)

	lock, err := s.GetLock()
	require.NoError(t, err)
	assert.Equal(t, "test-session", lock.Holder)
	assert.Equal(t, 1234, lock.PID)
	assert.False(t, lock.AcquiredAt.IsZero())
}

func TestAcquireLock_AlreadyHeld(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.AcquireLock("session-1", 1234))
	err := s.AcquireLock("session-2", 5678)
	require.ErrorIs(t, err, cserrors.ErrLocked)
}

func TestReleaseLock(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.AcquireLock("test-session", 1234))
	err := s.ReleaseLock()
	require.NoError(t, err)

	_, err = s.GetLock()
	require.ErrorIs(t, err, cserrors.ErrNotFound)
}

func TestReleaseLock_NoLock(t *testing.T) {
	s := testStore(t)

	err := s.ReleaseLock()
	require.NoError(t, err)
}

func TestGetLock_NoLock(t *testing.T) {
	s := testStore(t)

	_, err := s.GetLock()
	require.ErrorIs(t, err, cserrors.ErrNotFound)
}

func TestAcquireLock_AfterRelease(t *testing.T) {
	s := testStore(t)

	require.NoError(t, s.AcquireLock("session-1", 1234))
	require.NoError(t, s.ReleaseLock())

	err := s.AcquireLock("session-2", 5678)
	require.NoError(t, err)

	lock, err := s.GetLock()
	require.NoError(t, err)
	assert.Equal(t, "session-2", lock.Holder)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/ -v -run "TestAcquire|TestRelease|TestGetLock"`
Expected: FAIL — methods not defined.

- [ ] **Step 3: Implement lock operations**

Add to `store.go`:

```go
func (s *Store) AcquireLock(holder string, pid int) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := s.queries.InsertLock(context.Background(), db.InsertLockParams{
		Holder:     holder,
		Pid:        int64(pid),
		AcquiredAt: now,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return cserrors.ErrLocked
		}
		return err
	}
	return nil
}

func (s *Store) ReleaseLock() error {
	return s.queries.DeleteLock(context.Background())
}

func (s *Store) GetLock() (*Lock, error) {
	row, err := s.queries.GetLock(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, cserrors.ErrNotFound
		}
		return nil, err
	}
	lock := &Lock{
		Holder: row.Holder,
		PID:    int(row.Pid),
	}
	t, err := time.Parse(time.RFC3339Nano, row.AcquiredAt)
	if err == nil {
		lock.AcquiredAt = t
	}
	return lock, nil
}

func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed") ||
		strings.Contains(err.Error(), "PRIMARY KEY constraint failed")
}
```

Add `"strings"` to the imports in `store.go` (if not already present).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -v -run "TestAcquire|TestRelease|TestGetLock"`
Expected: PASS — all tests pass.

- [ ] **Step 5: Run all store tests**

Run: `go test ./internal/store/ -v`
Expected: PASS — all store tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat: add store lock operations"
```

---

### Task 11: Agent — Types, Constructor, and Config Isolation

**Files:**
- Create: `internal/agent/agent.go`

- [ ] **Step 1: Write agent types, constructor, and config isolation helper**

```go
package agent

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type RunOpts struct {
	Prompt               string
	Model                string
	Mode                 string
	Effort               string
	Cwd                  string
	RelevantPaths        []string
	AddDirs              []string
	Continue             bool
	Resume               bool
	SessionID            string
	Force                bool
	AllowedTools         []string
	DisallowedTools      []string
	AppendSystemPrompt   string
	NoSessionPersistence bool
}

type RunResult struct {
	Output     string  `json:"result"`
	Stderr     string  `json:"-"`
	Model      string  `json:"model"`
	SessionID  string  `json:"session_id"`
	DurationMS int64   `json:"duration_ms"`
	NumTurns   int     `json:"num_turns"`
	CostUSD    float64 `json:"cost_usd"`
	IsError    bool    `json:"is_error"`
	Usage      *Usage  `json:"usage"`
}

type Usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
}

type Agent struct {
	path           string
	apiKey         string
	defaultModel   string
	permissionMode string
	timeout        time.Duration
	configDir      string
}

func New(path, apiKey, defaultModel, permissionMode string, timeout time.Duration) *Agent {
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".claude")
	}
	return &Agent{
		path:           path,
		apiKey:         apiKey,
		defaultModel:   defaultModel,
		permissionMode: permissionMode,
		timeout:        timeout,
		configDir:      configDir,
	}
}

func (a *Agent) setupConfigIsolation() (string, func(), error) {
	tmpDir, err := os.MkdirTemp("", "crest-spec-claude-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp config dir: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }

	srcInfo, err := os.Stat(a.configDir)
	if err != nil || !srcInfo.IsDir() {
		return tmpDir, cleanup, nil
	}

	entries, err := os.ReadDir(a.configDir)
	if err != nil {
		return tmpDir, cleanup, nil
	}

	for _, entry := range entries {
		src := filepath.Join(a.configDir, entry.Name())
		dst := filepath.Join(tmpDir, entry.Name())

		if entry.Name() == ".claude.json" {
			data, err := os.ReadFile(src)
			if err != nil {
				continue
			}
			os.WriteFile(dst, data, 0o600)
			continue
		}

		if entry.IsDir() {
			os.Symlink(src, dst)
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(src)
			if err == nil {
				os.Symlink(target, dst)
			}
			continue
		}

		os.Link(src, dst)
	}

	return tmpDir, cleanup, nil
}

func parseResult(stdout, stderr []byte) *RunResult {
	var result RunResult
	if err := json.Unmarshal(stdout, &result); err != nil {
		result.Output = string(stdout)
	}
	result.Stderr = string(stderr)
	return &result
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/agent/`
Expected: No output (success).

- [ ] **Step 3: Commit**

```bash
git add internal/agent/agent.go
git commit -m "feat: add agent types, constructor, and config isolation"
```

---

### Task 12: Agent — RunPrompt Implementation

**Files:**
- Modify: `internal/agent/agent.go`
- Create: `internal/agent/agent_test.go`

- [ ] **Step 1: Write failing tests for RunPrompt**

```go
package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFakeClaude(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	err := os.WriteFile(path, []byte(script), 0o755)
	require.NoError(t, err)
	return path
}

func TestRunPrompt_BasicExecution(t *testing.T) {
	fakePath := writeFakeClaude(t, `#!/bin/sh
echo '{"result":"hello world","model":"claude-sonnet-4-6","is_error":false}'
`)

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt: "say hello",
	})
	require.NoError(t, err)
	assert.Equal(t, "hello world", result.Output)
	assert.Equal(t, "claude-sonnet-4-6", result.Model)
	assert.False(t, result.IsError)
}

func TestRunPrompt_IsErrorTrue(t *testing.T) {
	fakePath := writeFakeClaude(t, `#!/bin/sh
echo '{"result":"something went wrong","is_error":true}'
`)

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt: "fail",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is_error")
	assert.NotNil(t, result)
	assert.True(t, result.IsError)
}

func TestRunPrompt_NonZeroExit(t *testing.T) {
	fakePath := writeFakeClaude(t, `#!/bin/sh
echo '{"result":"partial output","is_error":false}' >&1
echo "crash details" >&2
exit 1
`)

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt: "crash",
	})
	require.Error(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "partial output", result.Output)
	assert.Contains(t, result.Stderr, "crash details")
}

func TestRunPrompt_ModelOverride(t *testing.T) {
	fakePath := writeFakeClaude(t, `#!/bin/sh
# Echo all args so we can check them
echo '{"result":"ok"}' >&1
echo "$@" >&2
`)

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt: "test",
		Model:  "claude-opus-4-8",
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Output)
	assert.Contains(t, result.Stderr, "--model")
	assert.Contains(t, result.Stderr, "claude-opus-4-8")
}

func TestRunPrompt_LargePromptViaStdin(t *testing.T) {
	fakePath := writeFakeClaude(t, `#!/bin/sh
# Read from stdin and echo back the length
INPUT=$(cat)
echo "{\"result\":\"got ${#INPUT} bytes\"}"
`)

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	largePrompt := make([]byte, 9000)
	for i := range largePrompt {
		largePrompt[i] = 'A'
	}

	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt: string(largePrompt),
	})
	require.NoError(t, err)
	assert.Contains(t, result.Output, "9000")
}

func TestRunPrompt_ContextCancellation(t *testing.T) {
	fakePath := writeFakeClaude(t, `#!/bin/sh
sleep 30
echo '{"result":"should not reach"}'
`)

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.RunPrompt(ctx, RunOpts{
		Prompt: "test",
	})
	require.Error(t, err)
}

func TestRunPrompt_DisallowedTools(t *testing.T) {
	fakePath := writeFakeClaude(t, `#!/bin/sh
echo '{"result":"ok"}' >&1
echo "$@" >&2
`)

	a := New(fakePath, "", "claude-sonnet-4-6", "default", 0)
	result, err := a.RunPrompt(context.Background(), RunOpts{
		Prompt:          "test",
		DisallowedTools: []string{"Bash", "Read", "Edit"},
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Output)
	assert.Contains(t, result.Stderr, "--disallowedTools")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agent/ -v -run TestRunPrompt`
Expected: FAIL — `RunPrompt` not defined.

- [ ] **Step 3: Implement RunPrompt**

Add to `agent.go`:

```go
import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"syscall"
)

func (a *Agent) RunPrompt(ctx context.Context, opts RunOpts) (*RunResult, error) {
	args := []string{"--print", "--output-format", "json"}

	model := opts.Model
	if model == "" {
		model = a.defaultModel
	}
	if model != "" {
		args = append(args, "--model", model)
	}

	if a.permissionMode != "" {
		args = append(args, "--permission-mode", a.permissionMode)
	}

	if opts.Effort != "" {
		args = append(args, "--effort", opts.Effort)
	}

	for _, dir := range opts.AddDirs {
		args = append(args, "--add-dir", dir)
	}

	if opts.Continue {
		args = append(args, "--continue")
	}
	if opts.Resume {
		args = append(args, "--resume")
	}
	if opts.SessionID != "" {
		args = append(args, "--session-id", opts.SessionID)
	}
	if opts.Force {
		args = append(args, "--dangerously-skip-permissions")
	}

	if len(opts.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(opts.AllowedTools, ","))
	}
	if len(opts.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(opts.DisallowedTools, ","))
	}

	if opts.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.AppendSystemPrompt)
	}
	if opts.NoSessionPersistence {
		args = append(args, "--no-session-persistence")
	}

	useStdin := len(opts.Prompt) > 8192
	if !useStdin && opts.Prompt != "" {
		args = append(args, opts.Prompt)
	}

	tmpConfigDir, cleanup, err := a.setupConfigIsolation()
	if err != nil {
		return nil, fmt.Errorf("config isolation: %w", err)
	}
	defer cleanup()

	cmd := exec.CommandContext(ctx, a.path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	env := os.Environ()
	env = append(env, "CLAUDE_CONFIG_DIR="+tmpConfigDir)
	if a.apiKey != "" {
		env = append(env, "ANTHROPIC_API_KEY="+a.apiKey)
	}
	cmd.Env = env

	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}

	if useStdin {
		cmd.Stdin = strings.NewReader(opts.Prompt)
	}

	runErr := cmd.Run()

	result := parseResult(stdout.Bytes(), stderr.Bytes())

	if runErr != nil {
		return result, fmt.Errorf("claude exited with error: %w (stderr: %s)", runErr, result.Stderr)
	}

	if result.IsError {
		return result, fmt.Errorf("claude returned is_error: %s", result.Output)
	}

	return result, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/ -v -run TestRunPrompt`
Expected: PASS — all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/agent.go internal/agent/agent_test.go
git commit -m "feat: add agent RunPrompt with config isolation and process groups"
```

---

### Task 13: Agent — Read-Only Commands

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/agent_test.go`

- [ ] **Step 1: Write failing tests**

Add to `agent_test.go`:

```go
func TestModels(t *testing.T) {
	fakePath := writeFakeClaude(t, `#!/bin/sh
if [ "$1" = "models" ]; then
    echo "claude-opus-4-8, claude-sonnet-4-6, claude-haiku-4-5"
    exit 0
fi
echo "unexpected args: $@" >&2
exit 1
`)

	a := New(fakePath, "", "", "", 0)
	out, err := a.Models(context.Background())
	require.NoError(t, err)
	assert.Contains(t, out, "claude-sonnet-4-6")
}

func TestAbout(t *testing.T) {
	fakePath := writeFakeClaude(t, `#!/bin/sh
if [ "$1" = "--version" ]; then
    echo "claude-code v1.0.0"
    exit 0
fi
echo "unexpected args: $@" >&2
exit 1
`)

	a := New(fakePath, "", "", "", 0)
	out, err := a.About(context.Background())
	require.NoError(t, err)
	assert.Contains(t, out, "claude-code")
}

func TestStatus(t *testing.T) {
	fakePath := writeFakeClaude(t, `#!/bin/sh
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
    echo "Authenticated as user@example.com"
    exit 0
fi
echo "unexpected args: $@" >&2
exit 1
`)

	a := New(fakePath, "", "", "", 0)
	out, err := a.Status(context.Background())
	require.NoError(t, err)
	assert.Contains(t, out, "Authenticated")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agent/ -v -run "TestModels|TestAbout|TestStatus"`
Expected: FAIL — methods not defined.

- [ ] **Step 3: Implement read-only commands**

Add to `agent.go`:

```go
func (a *Agent) runSimple(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, a.path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w (stderr: %s)", args[0], err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (a *Agent) Models(ctx context.Context) (string, error) {
	return a.runSimple(ctx, "models")
}

func (a *Agent) About(ctx context.Context) (string, error) {
	return a.runSimple(ctx, "--version")
}

func (a *Agent) Status(ctx context.Context) (string, error) {
	return a.runSimple(ctx, "auth", "status")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/ -v -run "TestModels|TestAbout|TestStatus"`
Expected: PASS — all three tests pass.

- [ ] **Step 5: Run all agent tests**

Run: `go test ./internal/agent/ -v`
Expected: PASS — all agent tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/agent.go internal/agent/agent_test.go
git commit -m "feat: add agent read-only commands (Models, About, Status)"
```

---

### Task 14: Main.go — Startup Sequence and checkJob

**Files:**
- Create: `cmd/crest-spec/main.go`

- [ ] **Step 1: Write main.go**

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	if len(os.Args) >= 4 && os.Args[1] == "check" && os.Args[2] == "job" {
		checkJob(os.Args[3])
		return
	}

	for _, arg := range os.Args[1:] {
		if arg == "-h" || arg == "--help" {
			config.Help()
			os.Exit(0)
		}
	}

	cfg, err := config.New()
	if err != nil {
		config.Help()
		panic(fmt.Sprintf("config: %v", err))
	}

	dbPath := dbPath()
	s, err := store.New(dbPath)
	if err != nil {
		panic(fmt.Sprintf("store: %v", err))
	}
	defer s.Close()

	cleaned, err := s.CleanupOrphans(processAlive)
	if err != nil {
		log.Warn().Err(err).Msg("orphan cleanup failed")
	} else if cleaned > 0 {
		log.Info().Int("cleaned", cleaned).Msg("cleaned orphaned jobs")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	_ = agent.New(
		cfg.AgentPath,
		cfg.APIKey,
		cfg.DefaultModel,
		cfg.PermissionMode,
		cfg.Timeout,
	)

	log.Info().Str("db", dbPath).Msg("crest-spec ready (engine/mcp not yet wired)")
	<-ctx.Done()
	log.Info().Msg("shutting down")
}

func checkJob(id string) {
	dbPath := dbPath()
	s, err := store.New(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "store: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	s.CleanupOrphans(processAlive)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	job, err := s.WaitForCompletion(ctx, id)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wait: %v\n", err)
		os.Exit(1)
	}

	switch job.Status {
	case "completed":
		fmt.Println(job.Result)
		s.DeleteJob(id)
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "job %s: status=%s error=%s\n", id, job.Status, job.Error)
		s.DeleteJob(id)
		os.Exit(1)
	}
}

func dbPath() string {
	dir := filepath.Join(".", ".crest-spec")
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "state.db")
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil
}
```

- [ ] **Step 2: Verify it builds**

Run: `go build -o bin/crest-spec ./cmd/crest-spec`
Expected: Binary created at `bin/crest-spec`.

- [ ] **Step 3: Test help flag**

Run: `./bin/crest-spec --help; echo "exit: $?"`
Expected: Prints environment variable table, exits 0.

- [ ] **Step 4: Add bin/ to .gitignore**

Append to `.gitignore`:
```
bin/
.crest-spec/
```

- [ ] **Step 5: Commit**

```bash
git add cmd/crest-spec/main.go .gitignore
git commit -m "feat: add main.go with startup sequence and checkJob subcommand"
```

---

### Task 15: Makefile and Final Verification

**Files:**
- Create: `Makefile`

- [ ] **Step 1: Write the Makefile**

```makefile
.PHONY: all build install test fmt lint lint-fix mocks sqlc update clean

all: fmt test lint

build:
	go build -o bin/crest-spec ./cmd/crest-spec

install:
	go install ./cmd/crest-spec

test:
	go test ./...

fmt:
	go fmt ./...

lint:
	golangci-lint run

lint-fix:
	golangci-lint run --fix

mocks:
	go generate ./internal/mocks/...

sqlc:
	sqlc generate

update:
	go get -u ./...
	go mod tidy

clean:
	rm -rf bin/
```

- [ ] **Step 2: Run make test**

Run: `make test`
Expected: All tests pass across all packages.

- [ ] **Step 3: Run make build**

Run: `make build`
Expected: Binary created at `bin/crest-spec`.

- [ ] **Step 4: Run the full suite**

Run: `go test -race ./...`
Expected: All tests pass with race detector enabled.

- [ ] **Step 5: Commit**

```bash
git add Makefile
git commit -m "feat: add Makefile with build, test, lint targets"
```

- [ ] **Step 6: Final verification — all tests, build, and help**

```bash
make test
make build
./bin/crest-spec --help
```

Expected: Tests pass, binary builds, help output displays correctly.
