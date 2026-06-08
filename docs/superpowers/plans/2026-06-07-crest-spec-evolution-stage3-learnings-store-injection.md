# Evolution Pillar — Stage 3: Learnings Store + Injection — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`).

**Goal:** Persist craft-level learnings in SQLite and inject the relevant ones (scoped by language + resource-kind, top-N) into generation prompts at runtime via the existing `RuntimeContext` mechanism.

**Architecture:** New `learnings` table (migration 012) + sqlc queries → `internal/db` codegen → `internal/store` wrappers. `RuntimeContext` gains a `Learnings []string` field rendered by `InjectRuntimeContext`. `buildRuntimeContext` queries active learnings for `(project language, resource kind)` and fills it. Reflection (Stage 4) writes rows; this stage only reads + injects (plus the write API the reflection engine will call).

**Tech Stack:** Go, SQLite (modernc), sqlc v1.31, testify.

**Reference:** Design doc Components 3–4.

---

## Task 1: Migration + sqlc queries + codegen

**Files:**
- Create: `migrations/012_learnings.sql`
- Create: `sql/queries/learnings.sql`
- Regenerate: `internal/db/*` via `sqlc generate`

- [ ] **Step 1: Create the migration**

Create `migrations/012_learnings.sql`:

```sql
-- Craft-level learnings distilled from generation history, injected into prompts.
CREATE TABLE IF NOT EXISTS learnings (
    id                   TEXT PRIMARY KEY,
    scope_lang           TEXT NOT NULL DEFAULT '',
    scope_kind           TEXT NOT NULL DEFAULT '',
    text                 TEXT NOT NULL,
    rationale            TEXT NOT NULL DEFAULT '',
    source_generation_id TEXT,
    source_apply_id      TEXT,
    confidence           REAL NOT NULL DEFAULT 0.5,
    status               TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','retired','promoted')),
    times_applied        INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);
CREATE INDEX idx_learnings_scope ON learnings(scope_lang, scope_kind, status);
```

- [ ] **Step 2: Create the queries**

Create `sql/queries/learnings.sql`:

```sql
-- name: CreateLearning :exec
INSERT INTO learnings (id, scope_lang, scope_kind, text, rationale, source_generation_id, source_apply_id, confidence, status, times_applied, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListActiveLearnings :many
SELECT * FROM learnings
WHERE status = 'active'
  AND (scope_lang = '' OR scope_lang = ?)
  AND (scope_kind = '' OR scope_kind = ?)
ORDER BY confidence DESC, created_at DESC
LIMIT ?;

-- name: ListLearningsByStatus :many
SELECT * FROM learnings
WHERE status = ?
ORDER BY confidence DESC, created_at DESC;

-- name: UpdateLearningStatus :exec
UPDATE learnings SET status = ?, updated_at = ? WHERE id = ?;

-- name: IncrementLearningApplied :exec
UPDATE learnings SET times_applied = times_applied + 1, updated_at = ? WHERE id = ?;
```

- [ ] **Step 3: Regenerate sqlc code**

Run: `sqlc generate`
Expected: no errors; creates `internal/db/learnings.sql.go` and adds a `Learning` struct to `internal/db/models.go`.

- [ ] **Step 4: Confirm it compiles**

Run: `go build ./internal/db/`
Expected: success.

- [ ] **Step 5: Commit**

```bash
git add migrations/012_learnings.sql sql/queries/learnings.sql internal/db/
git commit -m "feat(db): learnings table + sqlc queries"
```

---

## Task 2: Store wrappers

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go` (or a new `learnings_test.go` in `package store`)

> **Pattern (follow existing AgentEvent wrappers in store.go):** define a `Learning` struct with `time.Time` fields, a `dbLearningToLearning(db.Learning) Learning` converter using `stringVal`/`parseTime`, and `*Store` methods using `s.queries`. Nullable columns (`source_generation_id`, `source_apply_id`) are `*string` in `db.Learning` (sqlc `emit_pointers_for_null_types`), so use `stringVal`/`stringPtr`.

- [ ] **Step 1: Write failing store tests**

Create `internal/store/learnings_test.go`:

```go
package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir() + "/test.db")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLearnings_CreateAndListByScope(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	require.NoError(t, s.CreateLearning(Learning{
		ID: "l1", ScopeLang: "rust", ScopeKind: "adapter",
		Text: "prefer blocking send", Confidence: 0.9, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, s.CreateLearning(Learning{
		ID: "l2", ScopeLang: "rust", ScopeKind: "", // global rust
		Text: "use crate:: paths", Confidence: 0.5, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, s.CreateLearning(Learning{
		ID: "l3", ScopeLang: "go", ScopeKind: "adapter",
		Text: "irrelevant", Confidence: 0.9, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}))

	got, err := s.ListActiveLearnings("rust", "adapter", 10)
	require.NoError(t, err)
	// l1 (rust/adapter) and l2 (rust/global) match; l3 (go) does not.
	require.Len(t, got, 2)
	assert.Equal(t, "l1", got[0].ID) // higher confidence first
	assert.Equal(t, "l2", got[1].ID)
}

func TestLearnings_RetireExcludesFromActive(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	require.NoError(t, s.CreateLearning(Learning{ID: "l1", ScopeLang: "rust", Text: "x", Status: "active", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, s.UpdateLearningStatus("l1", "retired"))
	got, err := s.ListActiveLearnings("rust", "adapter", 10)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestLearnings_IncrementApplied(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	require.NoError(t, s.CreateLearning(Learning{ID: "l1", ScopeLang: "rust", Text: "x", Status: "active", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, s.IncrementLearningApplied("l1"))
	rows, err := s.ListLearnings("active")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].TimesApplied)
}
```

If `Store` has no `Close()` method, check store.go for the actual cleanup method name and use it (or drop the cleanup if `New` needs none — but it opens a DB, so there should be a close; search `func (s *Store) Close`).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/store/ -run TestLearnings -v`
Expected: FAIL — `Learning`/`CreateLearning`/etc. undefined.

- [ ] **Step 3: Implement the wrappers**

Add to `internal/store/store.go` (near the AgentEvent section):

```go
type Learning struct {
	ID                 string
	ScopeLang          string
	ScopeKind          string
	Text               string
	Rationale          string
	SourceGenerationID string
	SourceApplyID      string
	Confidence         float64
	Status             string
	TimesApplied       int
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func dbLearningToLearning(l db.Learning) Learning {
	return Learning{
		ID:                 l.ID,
		ScopeLang:          l.ScopeLang,
		ScopeKind:          l.ScopeKind,
		Text:               l.Text,
		Rationale:          l.Rationale,
		SourceGenerationID: stringVal(l.SourceGenerationID),
		SourceApplyID:      stringVal(l.SourceApplyID),
		Confidence:         l.Confidence,
		Status:             l.Status,
		TimesApplied:       int(l.TimesApplied),
		CreatedAt:          parseTime(l.CreatedAt),
		UpdatedAt:          parseTime(l.UpdatedAt),
	}
}

// CreateLearning inserts a new learning.
func (s *Store) CreateLearning(l Learning) error {
	if l.Status == "" {
		l.Status = "active"
	}
	return s.queries.CreateLearning(context.Background(), db.CreateLearningParams{
		ID:                 l.ID,
		ScopeLang:          l.ScopeLang,
		ScopeKind:          l.ScopeKind,
		Text:               l.Text,
		Rationale:          l.Rationale,
		SourceGenerationID: stringPtr(l.SourceGenerationID),
		SourceApplyID:      stringPtr(l.SourceApplyID),
		Confidence:         l.Confidence,
		Status:             l.Status,
		TimesApplied:       int64(l.TimesApplied),
		CreatedAt:          l.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:          l.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
}

// ListActiveLearnings returns active learnings matching the language and kind
// (empty scope = applies to any), highest confidence first, capped at limit.
func (s *Store) ListActiveLearnings(lang, kind string, limit int) ([]Learning, error) {
	rows, err := s.queries.ListActiveLearnings(context.Background(), db.ListActiveLearningsParams{
		ScopeLang: lang,
		ScopeKind: kind,
		Limit:     int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Learning, len(rows))
	for i, r := range rows {
		out[i] = dbLearningToLearning(r)
	}
	return out, nil
}

// ListLearnings returns all learnings with the given status.
func (s *Store) ListLearnings(status string) ([]Learning, error) {
	rows, err := s.queries.ListLearningsByStatus(context.Background(), status)
	if err != nil {
		return nil, err
	}
	out := make([]Learning, len(rows))
	for i, r := range rows {
		out[i] = dbLearningToLearning(r)
	}
	return out, nil
}

// UpdateLearningStatus changes a learning's status (e.g. "retired", "promoted").
func (s *Store) UpdateLearningStatus(id, status string) error {
	return s.queries.UpdateLearningStatus(context.Background(), db.UpdateLearningStatusParams{
		Status:    status,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ID:        id,
	})
}

// IncrementLearningApplied bumps times_applied for a learning.
func (s *Store) IncrementLearningApplied(id string) error {
	return s.queries.IncrementLearningApplied(context.Background(), db.IncrementLearningAppliedParams{
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		ID:        id,
	})
}
```

> The exact `db.*Params` field names come from sqlc codegen — if a generated param struct names a field differently (e.g. positional `Column1`), inspect `internal/db/learnings.sql.go` and adapt. The query column order above is chosen so sqlc emits named fields.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/store/ -run TestLearnings -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Run the store package + build**

Run: `go test ./internal/store/ && go build ./...`
Expected: PASS / success.

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/learnings_test.go
git commit -m "feat(store): learnings CRUD wrappers"
```

---

## Task 3: Inject learnings into the runtime context

**Files:**
- Modify: `internal/prompt/context.go` — add `Learnings []string` + render section
- Test: `internal/prompt/context_test.go`
- Modify: `internal/spec/spec.go` — add `ListActiveLearnings` + `IncrementLearningApplied` to `specStore` interface
- Modify: `internal/spec/runtime.go` — populate `ctx.Learnings`
- Test: `internal/spec/runtime_test.go`

- [ ] **Step 1: Add the field + section to `context.go` (write test first)**

Add to `internal/prompt/context_test.go`:

```go
func TestInjectRuntimeContext_Learnings(t *testing.T) {
	out := InjectRuntimeContext("BASE", RuntimeContext{
		Learnings: []string{"prefer blocking send", "derive PartialEq manually for NaN floats"},
	})
	assert.Contains(t, out, "## Learnings From Past Runs")
	assert.Contains(t, out, "prefer blocking send")
	assert.Contains(t, out, "derive PartialEq manually for NaN floats")
}

func TestInjectRuntimeContext_NoLearnings(t *testing.T) {
	out := InjectRuntimeContext("BASE", RuntimeContext{})
	assert.NotContains(t, out, "## Learnings From Past Runs")
}
```

Run: `go test ./internal/prompt/ -run TestInjectRuntimeContext_Learnings -v` → FAIL (field missing).

- [ ] **Step 2: Implement in `context.go`**

Add `Learnings []string` to the `RuntimeContext` struct. In `InjectRuntimeContext`, add a section (place it after Dependencies/Notes and before `WaveErrors`):

```go
	if len(ctx.Learnings) > 0 {
		var b strings.Builder
		b.WriteString("## Learnings From Past Runs\n\n")
		b.WriteString("Apply these craft guidelines distilled from earlier generations:\n\n")
		for _, l := range ctx.Learnings {
			b.WriteString("- " + l + "\n")
		}
		sections = append(sections, b.String())
	}
```

Run the prompt tests: `go test ./internal/prompt/` → PASS.

- [ ] **Step 3: Extend the `specStore` interface**

In `internal/spec/spec.go`, add these two methods to the `specStore` interface:

```go
	ListActiveLearnings(lang, kind string, limit int) ([]store.Learning, error)
	IncrementLearningApplied(id string) error
```

- [ ] **Step 4: Populate learnings in `buildRuntimeContext` (write test first)**

Add to `internal/spec/runtime_test.go` a test using a fake store. If the existing tests already use a fake `specStore`, extend it; otherwise add a minimal fake implementing only the methods `buildRuntimeContext` calls. Concretely:

```go
func TestBuildRuntimeContext_InjectsLearnings(t *testing.T) {
	fake := &learningsFakeStore{
		learnings: []store.Learning{
			{ID: "l1", Text: "prefer blocking send", ScopeLang: "rust", ScopeKind: "adapter"},
		},
	}
	s := &Spec{store: fake, cfg: config.Config{SpecDir: t.TempDir() + "/spec"}}
	reg := &cuepkg.Registry{Project: &cuepkg.Project{Meta: cuepkg.Meta{Language: "rust"}}}
	res := cuepkg.Resource{ID: "adapter.Foo", Kind: "adapter"}

	ctx, err := s.buildRuntimeContext(res, reg, "apply1")
	require.NoError(t, err)
	require.Contains(t, ctx.Learnings, "prefer blocking send")
	assert.Equal(t, "rust", fake.gotLang)
	assert.Equal(t, "adapter", fake.gotKind)
}
```

The fake must satisfy the full `specStore` interface. The simplest approach: embed the interface so unimplemented methods panic if called, and override only what's needed:

```go
type learningsFakeStore struct {
	specStore // embedded nil interface — only override what buildRuntimeContext calls
	learnings []store.Learning
	gotLang   string
	gotKind   string
}

func (f *learningsFakeStore) ListActiveLearnings(lang, kind string, limit int) ([]store.Learning, error) {
	f.gotLang, f.gotKind = lang, kind
	return f.learnings, nil
}
func (f *learningsFakeStore) IncrementLearningApplied(id string) error { return nil }
// buildRuntimeContext also calls GetGeneratedFiles and GetNote for dependencies;
// with no dependencies on the resource those are never called, so the embedded
// nil interface is safe. If they ARE called, add no-op overrides returning empty.
func (f *learningsFakeStore) GetGeneratedFiles(string) ([]store.GeneratedFile, error) { return nil, nil }
func (f *learningsFakeStore) GetNote(string, string) (string, error) { return "", nil }
```

Run: `go test ./internal/spec/ -run TestBuildRuntimeContext_InjectsLearnings -v` → FAIL (buildRuntimeContext doesn't query learnings yet).

- [ ] **Step 5: Implement retrieval in `runtime.go`**

In `buildRuntimeContext` (after the module/dependency/notes population, before `return ctx, nil`), add:

```go
	// Inject craft-level learnings scoped to this language + resource kind.
	const learningsInjectionCap = 10
	lang := registry.Project.Meta.Language
	if lang != "" {
		learnings, err := s.store.ListActiveLearnings(lang, resource.Kind, learningsInjectionCap)
		if err == nil && len(learnings) > 0 {
			texts := make([]string, len(learnings))
			for i, l := range learnings {
				texts[i] = l.Text
				_ = s.store.IncrementLearningApplied(l.ID)
			}
			ctx.Learnings = texts
		}
	}
```

Run: `go test ./internal/spec/ -run TestBuildRuntimeContext_InjectsLearnings -v` → PASS.

- [ ] **Step 6: Run spec + prompt packages**

Run: `go test ./internal/spec/ ./internal/prompt/`
Expected: PASS (the new fake must satisfy `specStore`; if other existing fakes in the spec package now fail to satisfy `specStore` because of the two new interface methods, add the two methods to those fakes too — search for types that implement `specStore`).

- [ ] **Step 7: Commit**

```bash
git add internal/prompt/context.go internal/prompt/context_test.go internal/spec/spec.go internal/spec/runtime.go internal/spec/runtime_test.go
git commit -m "feat(spec): inject scoped learnings into generation runtime context"
```

---

## Task 4: Full sweep

- [ ] `go build ./...` → success
- [ ] `go test ./...` → all pass
- [ ] `make build` → binary
- [ ] `git status` clean for this stage

---

## Self-Review

1. **Spec coverage (Components 3–4):** learnings table + migration ✔; sqlc queries + store CRUD ✔; `RuntimeContext.Learnings` + section ✔; scoped (lang+kind, global via empty) top-N retrieval with cap ✔; `times_applied` incremented on injection ✔.
2. **No placeholders:** exact SQL, Go, commands. ✔
3. **Type consistency:** `Learning` struct fields used identically across store + tests; `ListActiveLearnings(lang, kind string, limit int)` signature matches interface + runtime call + store impl. ✔
4. **Interface fallout:** Step 6 explicitly handles other `specStore` implementers needing the two new methods. ✔

## Notes
- Adding methods to `specStore` will break any other fake/mock implementing it — add the two new methods to each (they can be no-ops/panics). This is the main integration risk; the implementer must grep for `specStore` implementers.
- Reflection (Stage 4) is the writer; this stage ships read+inject + the write API it will call. Do NOT build the reflection engine here (YAGNI).
- Injection cap is a `const` (10) for now; making it configurable is deferred.
