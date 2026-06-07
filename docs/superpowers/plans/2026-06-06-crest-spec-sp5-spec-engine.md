# SP5: Spec Engine & Constraint Loop — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the spec engine that wires SP1–SP4 into a working system: store extensions, code block parser, validation runner, constraint loop, session management, and all 23 spec/* MCP tool handlers.

**Architecture:** New `internal/spec/` package orchestrates the plan/apply lifecycle. Store extensions add apply/generation/session CRUD. MCP tool stubs are replaced with real handlers that delegate to spec methods.

**Tech Stack:** Go 1.26.4, sqlc, SQLite (modernc.org/sqlite), existing engine/store/prompt/cue/graph/plan packages

---

## File Structure

### New files

| File | Responsibility |
|------|---------------|
| `sql/queries/applies.sql` | Apply + apply_action queries |
| `sql/queries/generations.sql` | Generation tracking queries |
| `sql/queries/sessions.sql` | Session + agent_note queries |
| `internal/spec/spec.go` | Spec engine struct, interfaces, constructor |
| `internal/spec/spec_test.go` | Spec engine tests |
| `internal/spec/parse.go` | Code block parser |
| `internal/spec/parse_test.go` | Parser tests |
| `internal/spec/validate.go` | Validation runner (subprocess commands) |
| `internal/spec/validate_test.go` | Validation tests |
| `internal/spec/loop.go` | Constraint loop |
| `internal/spec/loop_test.go` | Loop tests |
| `internal/spec/state.go` | Resource state machine types |
| `internal/spec/state_test.go` | State machine tests |
| `internal/spec/session.go` | Interactive session (begin/next/context/commit/finish) |
| `internal/spec/session_test.go` | Session tests |
| `internal/spec/resolve.go` | Resolution operations (resolve/amend/skip) |
| `internal/spec/query.go` | Read-only query operations |
| `internal/spec/runtime.go` | Runtime context builder |
| `internal/spec/fs.go` | File system abstraction |
| `internal/spec/apply.go` | Automated apply operation |

### Modified files

| File | Changes |
|------|---------|
| `internal/store/store.go` | Add apply, generation, session, note methods |
| `internal/mcp/server.go` | Add `spec` field, update `New()` signature |
| `internal/mcp/tools.go` | Replace 23 spec/* stubs with real handlers |
| `internal/mcp/handlers.go` | Implement resources/read, prompts/list, prompts/get |
| `cmd/crest-spec/main.go` | Create Spec, pass to MCP server |

---

## Task 1: Store layer — SQL queries and store methods

**Files:**
- Create: `sql/queries/applies.sql`
- Create: `sql/queries/generations.sql`
- Create: `sql/queries/sessions.sql`
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write applies.sql**

Create `sql/queries/applies.sql`:

```sql
-- name: CreateApply :exec
INSERT INTO applies (id, spec_hash, started_at)
VALUES (?, ?, ?);

-- name: GetApply :one
SELECT * FROM applies WHERE id = ?;

-- name: CompleteApply :execresult
UPDATE applies SET status = 'completed', done_at = ?
WHERE id = ? AND status = 'running';

-- name: FailApply :execresult
UPDATE applies SET status = 'failed', done_at = ?
WHERE id = ? AND status = 'running';

-- name: ListApplies :many
SELECT * FROM applies ORDER BY started_at DESC LIMIT ?;

-- name: CreateApplyAction :exec
INSERT INTO apply_actions (id, apply_id, resource_id, action, started_at)
VALUES (?, ?, ?, ?, ?);

-- name: UpdateApplyAction :exec
UPDATE apply_actions SET outcome = ?, error = ?, done_at = ?
WHERE id = ?;

-- name: ListApplyActions :many
SELECT * FROM apply_actions WHERE apply_id = ? ORDER BY started_at;
```

- [ ] **Step 2: Write generations.sql**

Create `sql/queries/generations.sql`:

```sql
-- name: CreateGeneration :exec
INSERT INTO generations (id, apply_id, resource_id, prompt_text, prompt_hash, model, retry_count, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateGeneration :exec
UPDATE generations SET output_text = ?, outcome = ?, rejection_reason = ?,
    duration_ms = ?, input_tokens = ?, output_tokens = ?, cost_usd = ?
WHERE id = ?;

-- name: ListGenerations :many
SELECT * FROM generations WHERE resource_id = ?
ORDER BY created_at DESC LIMIT ?;

-- name: GetGeneration :one
SELECT * FROM generations WHERE id = ?;
```

- [ ] **Step 3: Write sessions.sql**

Create `sql/queries/sessions.sql`:

```sql
-- name: CreateSession :exec
INSERT INTO agent_sessions (id, plan_json, waves_json, hashes_json, status, created_at, updated_at)
VALUES (?, ?, ?, ?, 'active', ?, ?);

-- name: GetSession :one
SELECT * FROM agent_sessions WHERE id = ?;

-- name: GetActiveSession :one
SELECT * FROM agent_sessions WHERE status = 'active' LIMIT 1;

-- name: UpdateSessionStatus :exec
UPDATE agent_sessions SET status = ?, current_wave = ?, updated_at = ?
WHERE id = ?;

-- name: CreateNote :exec
INSERT INTO agent_notes (resource_id, apply_id, content, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(resource_id, apply_id) DO UPDATE SET content = excluded.content, created_at = excluded.created_at;

-- name: GetNote :one
SELECT * FROM agent_notes WHERE resource_id = ? AND apply_id = ?;

-- name: ListNotes :many
SELECT * FROM agent_notes WHERE apply_id = ?;
```

- [ ] **Step 4: Run sqlc to generate Go code**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && sqlc generate
```

Expected: New files generated in `internal/db/`: `applies.sql.go`, `generations.sql.go`, `sessions.sql.go`.

- [ ] **Step 5: Add store domain types and methods**

Add to `internal/store/store.go` after the existing Dependency type and before `Store struct`:

```go
// Apply is the store's domain type for an apply record.
type Apply struct {
	ID        string
	Status    string
	SpecHash  string
	StartedAt time.Time
	DoneAt    *time.Time
}

// ApplyAction is the store's domain type for a per-resource action within an apply.
type ApplyAction struct {
	ID         string
	ApplyID    string
	ResourceID string
	Action     string
	Outcome    string
	Error      string
	StartedAt  time.Time
	DoneAt     *time.Time
}

// Generation is the store's domain type for an LLM generation record.
type Generation struct {
	ID              string
	ApplyID         string
	ResourceID      string
	PromptText      string
	PromptHash      string
	OutputText      string
	Model           string
	Outcome         string
	RejectionReason string
	RetryCount      int
	DurationMS      int64
	InputTokens     int64
	OutputTokens    int64
	CostUSD         float64
	CreatedAt       time.Time
}

// Session is the store's domain type for an agent session.
type Session struct {
	ID          string
	PlanJSON    string
	WavesJSON   string
	HashesJSON  string
	CurrentWave int
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// AgentNote is the store's domain type for a per-resource note.
type AgentNote struct {
	ResourceID string
	ApplyID    string
	Content    string
	CreatedAt  time.Time
}
```

Then add converter functions and CRUD methods at the end of the file:

```go
// ---------------------------------------------------------------------------
// Apply CRUD
// ---------------------------------------------------------------------------

func dbApplyToApply(a db.Apply) Apply {
	out := Apply{
		ID:       a.ID,
		Status:   a.Status,
		SpecHash: a.SpecHash,
	}
	if t, err := time.Parse(time.RFC3339Nano, a.StartedAt); err == nil {
		out.StartedAt = t
	}
	if a.DoneAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *a.DoneAt); err == nil {
			out.DoneAt = &t
		}
	}
	return out
}

func (s *Store) CreateApply(id, specHash string) error {
	return s.queries.CreateApply(context.Background(), db.CreateApplyParams{
		ID:        id,
		SpecHash:  specHash,
		StartedAt: now(),
	})
}

func (s *Store) GetApply(id string) (*Apply, error) {
	a, err := s.queries.GetApply(context.Background(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, cserrors.ErrNotFound
		}
		return nil, err
	}
	out := dbApplyToApply(a)
	return &out, nil
}

func (s *Store) CompleteApply(id string) error {
	ts := now()
	res, err := s.queries.CompleteApply(context.Background(), db.CompleteApplyParams{
		DoneAt: &ts,
		ID:     id,
	})
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return cserrors.ErrAlreadyDone
	}
	return nil
}

func (s *Store) FailApply(id string) error {
	ts := now()
	res, err := s.queries.FailApply(context.Background(), db.FailApplyParams{
		DoneAt: &ts,
		ID:     id,
	})
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return cserrors.ErrAlreadyDone
	}
	return nil
}

func (s *Store) ListApplies(limit int) ([]Apply, error) {
	rows, err := s.queries.ListApplies(context.Background(), int64(limit))
	if err != nil {
		return nil, err
	}
	out := make([]Apply, len(rows))
	for i, a := range rows {
		out[i] = dbApplyToApply(a)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// ApplyAction CRUD
// ---------------------------------------------------------------------------

func dbApplyActionToApplyAction(a db.ApplyAction) ApplyAction {
	out := ApplyAction{
		ID:         a.ID,
		ApplyID:    a.ApplyID,
		ResourceID: a.ResourceID,
		Action:     a.Action,
	}
	if a.Outcome != nil {
		out.Outcome = *a.Outcome
	}
	if a.Error != nil {
		out.Error = *a.Error
	}
	if t, err := time.Parse(time.RFC3339Nano, a.StartedAt); err == nil {
		out.StartedAt = t
	}
	if a.DoneAt != nil {
		if t, err := time.Parse(time.RFC3339Nano, *a.DoneAt); err == nil {
			out.DoneAt = &t
		}
	}
	return out
}

func (s *Store) CreateApplyAction(id, applyID, resourceID, action string) error {
	return s.queries.CreateApplyAction(context.Background(), db.CreateApplyActionParams{
		ID:         id,
		ApplyID:    applyID,
		ResourceID: resourceID,
		Action:     action,
		StartedAt:  now(),
	})
}

func (s *Store) UpdateApplyAction(id, outcome, errMsg string) error {
	var errPtr *string
	if errMsg != "" {
		errPtr = &errMsg
	}
	ts := now()
	return s.queries.UpdateApplyAction(context.Background(), db.UpdateApplyActionParams{
		Outcome: &outcome,
		Error:   errPtr,
		DoneAt:  &ts,
		ID:      id,
	})
}

func (s *Store) ListApplyActions(applyID string) ([]ApplyAction, error) {
	rows, err := s.queries.ListApplyActions(context.Background(), applyID)
	if err != nil {
		return nil, err
	}
	out := make([]ApplyAction, len(rows))
	for i, a := range rows {
		out[i] = dbApplyActionToApplyAction(a)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Generation CRUD
// ---------------------------------------------------------------------------

func dbGenerationToGeneration(g db.Generation) Generation {
	out := Generation{
		ID:         g.ID,
		ResourceID: g.ResourceID,
		PromptText: g.PromptText,
		PromptHash: g.PromptHash,
		Model:      g.Model,
		RetryCount: int(g.RetryCount),
	}
	if g.ApplyID != nil {
		out.ApplyID = *g.ApplyID
	}
	if g.OutputText != nil {
		out.OutputText = *g.OutputText
	}
	if g.Outcome != nil {
		out.Outcome = *g.Outcome
	}
	if g.RejectionReason != nil {
		out.RejectionReason = *g.RejectionReason
	}
	if g.DurationMs != nil {
		out.DurationMS = *g.DurationMs
	}
	if g.InputTokens != nil {
		out.InputTokens = *g.InputTokens
	}
	if g.OutputTokens != nil {
		out.OutputTokens = *g.OutputTokens
	}
	if g.CostUsd != nil {
		out.CostUSD = *g.CostUsd
	}
	if t, err := time.Parse(time.RFC3339Nano, g.CreatedAt); err == nil {
		out.CreatedAt = t
	}
	return out
}

func (s *Store) CreateGeneration(g Generation) error {
	var applyID *string
	if g.ApplyID != "" {
		applyID = &g.ApplyID
	}
	return s.queries.CreateGeneration(context.Background(), db.CreateGenerationParams{
		ID:         g.ID,
		ApplyID:    applyID,
		ResourceID: g.ResourceID,
		PromptText: g.PromptText,
		PromptHash: g.PromptHash,
		Model:      g.Model,
		RetryCount: int64(g.RetryCount),
		CreatedAt:  now(),
	})
}

func (s *Store) UpdateGeneration(id string, outputText, outcome, rejectionReason string, durationMS, inputTokens, outputTokens int64, costUSD float64) error {
	var rejPtr *string
	if rejectionReason != "" {
		rejPtr = &rejectionReason
	}
	return s.queries.UpdateGeneration(context.Background(), db.UpdateGenerationParams{
		OutputText:      &outputText,
		Outcome:         &outcome,
		RejectionReason: rejPtr,
		DurationMs:      &durationMS,
		InputTokens:     &inputTokens,
		OutputTokens:    &outputTokens,
		CostUsd:         &costUSD,
		ID:              id,
	})
}

func (s *Store) ListGenerations(resourceID string, limit int) ([]Generation, error) {
	rows, err := s.queries.ListGenerations(context.Background(), db.ListGenerationsParams{
		ResourceID: resourceID,
		Limit:      int64(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Generation, len(rows))
	for i, g := range rows {
		out[i] = dbGenerationToGeneration(g)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Session CRUD
// ---------------------------------------------------------------------------

func dbSessionToSession(s db.AgentSession) Session {
	out := Session{
		ID:          s.ID,
		PlanJSON:    s.PlanJson,
		WavesJSON:   s.WavesJson,
		HashesJSON:  s.HashesJson,
		CurrentWave: int(s.CurrentWave),
		Status:      s.Status,
	}
	if t, err := time.Parse(time.RFC3339Nano, s.CreatedAt); err == nil {
		out.CreatedAt = t
	}
	if t, err := time.Parse(time.RFC3339Nano, s.UpdatedAt); err == nil {
		out.UpdatedAt = t
	}
	return out
}

func (s *Store) CreateSession(sess Session) error {
	return s.queries.CreateSession(context.Background(), db.CreateSessionParams{
		ID:         sess.ID,
		PlanJson:   sess.PlanJSON,
		WavesJson:  sess.WavesJSON,
		HashesJson: sess.HashesJSON,
		CreatedAt:  now(),
		UpdatedAt:  now(),
	})
}

func (s *Store) GetSession(id string) (*Session, error) {
	sess, err := s.queries.GetSession(context.Background(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, cserrors.ErrNotFound
		}
		return nil, err
	}
	out := dbSessionToSession(sess)
	return &out, nil
}

func (s *Store) GetActiveSession() (*Session, error) {
	sess, err := s.queries.GetActiveSession(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, cserrors.ErrNotFound
		}
		return nil, err
	}
	out := dbSessionToSession(sess)
	return &out, nil
}

func (s *Store) UpdateSession(id, status string, currentWave int) error {
	return s.queries.UpdateSessionStatus(context.Background(), db.UpdateSessionStatusParams{
		Status:      status,
		CurrentWave: int64(currentWave),
		UpdatedAt:   now(),
		ID:          id,
	})
}

// ---------------------------------------------------------------------------
// Note CRUD
// ---------------------------------------------------------------------------

func (s *Store) SetNote(resourceID, applyID, content string) error {
	return s.queries.CreateNote(context.Background(), db.CreateNoteParams{
		ResourceID: resourceID,
		ApplyID:    applyID,
		Content:    content,
		CreatedAt:  now(),
	})
}

func (s *Store) GetNote(resourceID, applyID string) (string, error) {
	note, err := s.queries.GetNote(context.Background(), db.GetNoteParams{
		ResourceID: resourceID,
		ApplyID:    applyID,
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return note.Content, nil
}

func (s *Store) ListNotes(applyID string) ([]AgentNote, error) {
	rows, err := s.queries.ListNotes(context.Background(), applyID)
	if err != nil {
		return nil, err
	}
	out := make([]AgentNote, len(rows))
	for i, n := range rows {
		out[i] = AgentNote{
			ResourceID: n.ResourceID,
			ApplyID:    n.ApplyID,
			Content:    n.Content,
		}
		if t, err := time.Parse(time.RFC3339Nano, n.CreatedAt); err == nil {
			out[i].CreatedAt = t
		}
	}
	return out, nil
}
```

- [ ] **Step 6: Write store tests**

Add to `internal/store/store_test.go`:

```go
func TestApplyCRUD(t *testing.T) {
	s := newTestStore(t)

	err := s.CreateApply("apply-1", "hash-abc")
	require.NoError(t, err)

	a, err := s.GetApply("apply-1")
	require.NoError(t, err)
	assert.Equal(t, "running", a.Status)
	assert.Equal(t, "hash-abc", a.SpecHash)

	err = s.CompleteApply("apply-1")
	require.NoError(t, err)

	a, err = s.GetApply("apply-1")
	require.NoError(t, err)
	assert.Equal(t, "completed", a.Status)
	assert.NotNil(t, a.DoneAt)

	err = s.CompleteApply("apply-1")
	assert.ErrorIs(t, err, cserrors.ErrAlreadyDone)
}

func TestApplyActionCRUD(t *testing.T) {
	s := newTestStore(t)
	require.NoError(t, s.CreateApply("apply-1", "hash"))

	err := s.CreateApplyAction("action-1", "apply-1", "aggregate.Synth.Voice", "create")
	require.NoError(t, err)

	err = s.UpdateApplyAction("action-1", "committed", "")
	require.NoError(t, err)

	actions, err := s.ListApplyActions("apply-1")
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "committed", actions[0].Outcome)
}

func TestGenerationCRUD(t *testing.T) {
	s := newTestStore(t)
	require.NoError(t, s.CreateApply("apply-1", "hash"))

	gen := store.Generation{
		ID:         "gen-1",
		ApplyID:    "apply-1",
		ResourceID: "aggregate.Synth.Voice",
		PromptText: "generate voice",
		PromptHash: "phash",
		Model:      "claude-sonnet-4-6",
		RetryCount: 0,
	}
	err := s.CreateGeneration(gen)
	require.NoError(t, err)

	err = s.UpdateGeneration("gen-1", "code output", "accepted", "", 1500, 100, 200, 0.01)
	require.NoError(t, err)

	gens, err := s.ListGenerations("aggregate.Synth.Voice", 10)
	require.NoError(t, err)
	require.Len(t, gens, 1)
	assert.Equal(t, "accepted", gens[0].Outcome)
	assert.Equal(t, int64(1500), gens[0].DurationMS)
}

func TestSessionCRUD(t *testing.T) {
	s := newTestStore(t)

	sess := store.Session{
		ID:         "sess-1",
		PlanJSON:   `[{"id":"a"}]`,
		WavesJSON:  `[["a"]]`,
		HashesJSON: `{"a":"h1"}`,
	}
	err := s.CreateSession(sess)
	require.NoError(t, err)

	got, err := s.GetSession("sess-1")
	require.NoError(t, err)
	assert.Equal(t, "active", got.Status)

	got, err = s.GetActiveSession()
	require.NoError(t, err)
	assert.Equal(t, "sess-1", got.ID)

	err = s.UpdateSession("sess-1", "completed", 2)
	require.NoError(t, err)

	got, err = s.GetSession("sess-1")
	require.NoError(t, err)
	assert.Equal(t, "completed", got.Status)
	assert.Equal(t, 2, got.CurrentWave)
}

func TestNoteCRUD(t *testing.T) {
	s := newTestStore(t)

	err := s.SetNote("aggregate.Synth.Voice", "apply-1", "used newtype wrappers")
	require.NoError(t, err)

	content, err := s.GetNote("aggregate.Synth.Voice", "apply-1")
	require.NoError(t, err)
	assert.Equal(t, "used newtype wrappers", content)

	// Missing note returns empty string
	content, err = s.GetNote("nonexistent", "apply-1")
	require.NoError(t, err)
	assert.Equal(t, "", content)

	// Upsert
	err = s.SetNote("aggregate.Synth.Voice", "apply-1", "updated note")
	require.NoError(t, err)

	content, err = s.GetNote("aggregate.Synth.Voice", "apply-1")
	require.NoError(t, err)
	assert.Equal(t, "updated note", content)

	notes, err := s.ListNotes("apply-1")
	require.NoError(t, err)
	require.Len(t, notes, 1)
}

func TestListApplies(t *testing.T) {
	s := newTestStore(t)

	require.NoError(t, s.CreateApply("a1", "h1"))
	require.NoError(t, s.CreateApply("a2", "h2"))

	applies, err := s.ListApplies(10)
	require.NoError(t, err)
	assert.Len(t, applies, 2)
}

func TestFailApply(t *testing.T) {
	s := newTestStore(t)

	require.NoError(t, s.CreateApply("a1", "h1"))

	err := s.FailApply("a1")
	require.NoError(t, err)

	a, err := s.GetApply("a1")
	require.NoError(t, err)
	assert.Equal(t, "failed", a.Status)
}
```

- [ ] **Step 7: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/store/ -v -run "TestApply|TestGeneration|TestSession|TestNote|TestList|TestFail"
```

Expected: All new tests pass.

- [ ] **Step 8: Run full suite**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./...
```

Expected: All tests pass.

- [ ] **Step 9: Commit**

```bash
git add sql/queries/applies.sql sql/queries/generations.sql sql/queries/sessions.sql internal/db/ internal/store/store.go internal/store/store_test.go
git commit -m "feat(sp5): add store layer for applies, generations, sessions, and notes"
```

---

## Task 2: Code block parser

**Files:**
- Create: `internal/spec/parse.go`
- Create: `internal/spec/parse_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/spec/parse_test.go`:

```go
package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCodeBlocks_SingleBlock(t *testing.T) {
	output := "Here is the code:\n\n```rust\n// path: src/Synth/Voice.rs\npub struct Voice {\n    frequency: f64,\n}\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "src/Synth/Voice.rs", blocks[0].Path)
	assert.Contains(t, blocks[0].Content, "pub struct Voice")
	assert.Equal(t, "rust", blocks[0].Lang)
}

func TestParseCodeBlocks_MultipleBlocks(t *testing.T) {
	output := "```go\n// path: src/Synth/Voice/voice.go\npackage voice\n```\n\n```go\n// path: src/Synth/Voice/voice_test.go\npackage voice_test\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	assert.Equal(t, "src/Synth/Voice/voice.go", blocks[0].Path)
	assert.Equal(t, "src/Synth/Voice/voice_test.go", blocks[1].Path)
}

func TestParseCodeBlocks_HashPathAnnotation(t *testing.T) {
	output := "```python\n# path: src/synth/voice.py\nclass Voice:\n    pass\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "src/synth/voice.py", blocks[0].Path)
}

func TestParseCodeBlocks_NoBlocks(t *testing.T) {
	output := "I'm sorry, I can't generate that code."

	blocks, err := ParseCodeBlocks(output)
	assert.Error(t, err)
	assert.Nil(t, blocks)
	assert.Contains(t, err.Error(), "no code blocks")
}

func TestParseCodeBlocks_BlockWithoutPath(t *testing.T) {
	output := "```rust\npub struct Voice {}\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	assert.Equal(t, "", blocks[0].Path)
}

func TestParseCodeBlocks_MixedWithAndWithoutPath(t *testing.T) {
	output := "Some text\n```rust\n// path: src/voice.rs\ncode1\n```\nMore text\n```\ncode2\n```\n"

	blocks, err := ParseCodeBlocks(output)
	require.NoError(t, err)
	require.Len(t, blocks, 2)
	assert.Equal(t, "src/voice.rs", blocks[0].Path)
	assert.Equal(t, "", blocks[1].Path)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/spec/ -v
```

Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement parse.go**

Create `internal/spec/parse.go`:

```go
package spec

import (
	"fmt"
	"regexp"
	"strings"
)

type CodeBlock struct {
	Path    string
	Content string
	Lang    string
}

var (
	fenceOpenRe = regexp.MustCompile("^```(\\w*)\\s*$")
	pathRe      = regexp.MustCompile(`^(?://|#)\s*path:\s*(.+)$`)
)

func ParseCodeBlocks(output string) ([]CodeBlock, error) {
	lines := strings.Split(output, "\n")
	var blocks []CodeBlock
	var current *CodeBlock
	var contentLines []string

	for _, line := range lines {
		if current == nil {
			if m := fenceOpenRe.FindStringSubmatch(line); m != nil {
				current = &CodeBlock{Lang: m[1]}
				contentLines = nil
			}
			continue
		}

		if strings.TrimSpace(line) == "```" {
			current.Content = strings.Join(contentLines, "\n")
			blocks = append(blocks, *current)
			current = nil
			continue
		}

		if len(contentLines) == 0 {
			if m := pathRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				current.Path = strings.TrimSpace(m[1])
				continue
			}
		}

		contentLines = append(contentLines, line)
	}

	if len(blocks) == 0 {
		return nil, fmt.Errorf("no code blocks found in output")
	}

	return blocks, nil
}
```

- [ ] **Step 4: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/spec/ -v
```

Expected: All 6 tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/spec/parse.go internal/spec/parse_test.go
git commit -m "feat(sp5): implement code block parser"
```

---

## Task 3: Validation runner and file system abstraction

**Files:**
- Create: `internal/spec/fs.go`
- Create: `internal/spec/validate.go`
- Create: `internal/spec/validate_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/spec/validate_test.go`:

```go
package spec

import (
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunCommand_Success(t *testing.T) {
	stdout, stderr, exitCode, err := RunCommand(t.Context(), []string{"echo", "hello"}, ".")
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
	assert.Contains(t, stdout, "hello")
	assert.Empty(t, stderr)
}

func TestRunCommand_Failure(t *testing.T) {
	_, _, exitCode, err := RunCommand(t.Context(), []string{"false"}, ".")
	require.NoError(t, err)
	assert.NotEqual(t, 0, exitCode)
}

func TestCheckAssertions_ExitCode(t *testing.T) {
	assertions := []cuepkg.Assertion{
		{Kind: "exit_code", Expected: 0},
	}
	results := CheckAssertions(assertions, "", "", 0)
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)

	results = CheckAssertions(assertions, "", "", 1)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

func TestCheckAssertions_StdoutContains(t *testing.T) {
	assertions := []cuepkg.Assertion{
		{Kind: "stdout_contains", Pattern: "hello world"},
	}
	results := CheckAssertions(assertions, "the output says hello world here", "", 0)
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)

	results = CheckAssertions(assertions, "nothing here", "", 0)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

func TestCheckAssertions_FileExists(t *testing.T) {
	assertions := []cuepkg.Assertion{
		{Kind: "file_exists", Path: "validate_test.go"},
	}
	results := CheckAssertions(assertions, "", "", 0)
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)

	assertions = []cuepkg.Assertion{
		{Kind: "file_exists", Path: "nonexistent.txt"},
	}
	results = CheckAssertions(assertions, "", "", 0)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}

func TestRunValidations_NoValidations(t *testing.T) {
	results, err := RunValidations(t.Context(), nil, ".")
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestRunValidations_CompileSuccess(t *testing.T) {
	validations := []cuepkg.Validation{
		{Kind: "compiles", Command: []string{"true"}},
	}
	results, err := RunValidations(t.Context(), validations, ".")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.True(t, results[0].Passed)
}

func TestRunValidations_CompileFailure(t *testing.T) {
	validations := []cuepkg.Validation{
		{Kind: "compiles", Command: []string{"false"}},
	}
	results, err := RunValidations(t.Context(), validations, ".")
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.False(t, results[0].Passed)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/spec/ -run "TestRun|TestCheck" -v
```

Expected: FAIL.

- [ ] **Step 3: Implement fs.go**

Create `internal/spec/fs.go`:

```go
package spec

import (
	"io/fs"
	"os"
)

type fileSystem interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm fs.FileMode) error
	MkdirAll(path string, perm fs.FileMode) error
	Remove(path string) error
	ReadDir(path string) ([]os.DirEntry, error)
	Stat(path string) (fs.FileInfo, error)
}

type OSFileSystem struct{}

func (OSFileSystem) ReadFile(path string) ([]byte, error)                     { return os.ReadFile(path) }
func (OSFileSystem) WriteFile(path string, data []byte, perm fs.FileMode) error { return os.WriteFile(path, data, perm) }
func (OSFileSystem) MkdirAll(path string, perm fs.FileMode) error            { return os.MkdirAll(path, perm) }
func (OSFileSystem) Remove(path string) error                                { return os.Remove(path) }
func (OSFileSystem) ReadDir(path string) ([]os.DirEntry, error)              { return os.ReadDir(path) }
func (OSFileSystem) Stat(path string) (fs.FileInfo, error)                   { return os.Stat(path) }
```

- [ ] **Step 4: Implement validate.go**

Create `internal/spec/validate.go`:

```go
package spec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

type ValidationResult struct {
	Passed  bool
	Kind    string
	Message string
}

func RunCommand(ctx context.Context, command []string, cwd string) (stdout, stderr string, exitCode int, err error) {
	if len(command) == 0 {
		return "", "", -1, fmt.Errorf("empty command")
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = cwd

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return stdout, stderr, exitErr.ExitCode(), nil
		}
		return stdout, stderr, -1, runErr
	}

	return stdout, stderr, 0, nil
}

func CheckAssertions(assertions []cuepkg.Assertion, stdout, stderr string, exitCode int) []ValidationResult {
	var results []ValidationResult

	for _, a := range assertions {
		var r ValidationResult
		r.Kind = a.Kind

		switch a.Kind {
		case "exit_code":
			r.Passed = exitCode == a.Expected
			if !r.Passed {
				r.Message = fmt.Sprintf("expected exit code %d, got %d", a.Expected, exitCode)
			}
		case "stdout_contains":
			r.Passed = strings.Contains(stdout, a.Pattern)
			if !r.Passed {
				r.Message = fmt.Sprintf("stdout does not contain %q", a.Pattern)
			}
		case "stderr_empty":
			r.Passed = strings.TrimSpace(stderr) == ""
			if !r.Passed {
				r.Message = fmt.Sprintf("stderr not empty: %s", stderr)
			}
		case "file_exists":
			_, err := os.Stat(a.Path)
			r.Passed = err == nil
			if !r.Passed {
				r.Message = fmt.Sprintf("file does not exist: %s", a.Path)
			}
		case "file_not_empty":
			info, err := os.Stat(a.Path)
			r.Passed = err == nil && info.Size() > 0
			if !r.Passed {
				r.Message = fmt.Sprintf("file empty or missing: %s", a.Path)
			}
		default:
			r.Passed = false
			r.Message = fmt.Sprintf("unknown assertion kind: %s", a.Kind)
		}

		results = append(results, r)
	}

	return results
}

func RunValidations(ctx context.Context, validations []cuepkg.Validation, cwd string) ([]ValidationResult, error) {
	var results []ValidationResult

	for _, v := range validations {
		stdout, stderr, exitCode, err := RunCommand(ctx, v.Command, cwd)
		if err != nil {
			return nil, fmt.Errorf("run validation %s: %w", v.Kind, err)
		}

		switch v.Kind {
		case "compiles", "test", "custom":
			passed := exitCode == 0
			msg := ""
			if !passed {
				msg = fmt.Sprintf("%s failed (exit %d):\nstdout: %s\nstderr: %s", v.Kind, exitCode, stdout, stderr)
			}
			results = append(results, ValidationResult{
				Passed:  passed,
				Kind:    v.Kind,
				Message: msg,
			})

		case "integration":
			if len(v.Assertions) > 0 {
				assertionResults := CheckAssertions(v.Assertions, stdout, stderr, exitCode)
				allPassed := true
				var msgs []string
				for _, ar := range assertionResults {
					if !ar.Passed {
						allPassed = false
						msgs = append(msgs, ar.Message)
					}
				}
				msg := ""
				if !allPassed {
					msg = strings.Join(msgs, "; ")
				}
				results = append(results, ValidationResult{
					Passed:  allPassed,
					Kind:    v.Kind,
					Message: msg,
				})
			} else {
				passed := exitCode == 0
				msg := ""
				if !passed {
					msg = fmt.Sprintf("integration failed (exit %d):\nstdout: %s\nstderr: %s", exitCode, stdout, stderr)
				}
				results = append(results, ValidationResult{
					Passed:  passed,
					Kind:    v.Kind,
					Message: msg,
				})
			}
		}

		if len(results) > 0 && !results[len(results)-1].Passed {
			break
		}
	}

	return results, nil
}
```

- [ ] **Step 5: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/spec/ -v
```

Expected: All tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/spec/fs.go internal/spec/validate.go internal/spec/validate_test.go
git commit -m "feat(sp5): implement validation runner and filesystem abstraction"
```

---

## Task 4: Resource state machine and spec engine struct

**Files:**
- Create: `internal/spec/state.go`
- Create: `internal/spec/state_test.go`
- Create: `internal/spec/spec.go`

- [ ] **Step 1: Write failing tests**

Create `internal/spec/state_test.go`:

```go
package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResourceState_IsTerminal(t *testing.T) {
	assert.True(t, StateCommitted.IsTerminal())
	assert.True(t, StateSkipped.IsTerminal())
	assert.False(t, StatePending.IsTerminal())
	assert.False(t, StateDispatched.IsTerminal())
	assert.False(t, StateErrored.IsTerminal())
}

func TestResourceState_NeedsResolution(t *testing.T) {
	assert.True(t, StateBlocked.NeedsResolution())
	assert.True(t, StateErrored.NeedsResolution())
	assert.True(t, StateTimedOut.NeedsResolution())
	assert.True(t, StateRejected.NeedsResolution())
	assert.False(t, StatePending.NeedsResolution())
	assert.False(t, StateCommitted.NeedsResolution())
}
```

- [ ] **Step 2: Implement state.go**

Create `internal/spec/state.go`:

```go
package spec

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

func (s ResourceState) IsTerminal() bool {
	return s == StateCommitted || s == StateSkipped
}

func (s ResourceState) NeedsResolution() bool {
	return s == StateBlocked || s == StateErrored || s == StateTimedOut || s == StateRejected
}

type ResourceStatus struct {
	ResourceID   string
	State        ResourceState
	WaveIndex    int
	Error        *ErrorContext
	Blocked      *BlockedContext
	Attempts     int
	MaxRetries   int
	Files        []CodeBlock
	Notes        string
	UserGuidance string
}

type ErrorContext struct {
	Kind        string
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

- [ ] **Step 3: Implement spec.go**

Create `internal/spec/spec.go`:

```go
package spec

import (
	"context"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/engine"
	graphpkg "github.com/crestenstclair/crest-spec/internal/graph"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type specEngine interface {
	Generate(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error)
	Review(ctx context.Context, opts engine.ReviewOpts) (*agent.RunResult, error)
	CodeReview(ctx context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error)
	Bugbot(ctx context.Context, opts engine.BugbotOpts) (*agent.RunResult, error)
}

type specStore interface {
	GetResource(id string) (*store.Resource, error)
	ListResources() ([]store.Resource, error)
	SetResource(r store.Resource) error
	DeleteResource(id string) error
	GetGeneratedFiles(resourceID string) ([]store.GeneratedFile, error)
	SetGeneratedFile(f store.GeneratedFile) error
	DeleteGeneratedFiles(resourceID string) error
	SetDependency(sourceID, targetID, kind string) error
	DeleteDependencies(sourceID string) error
	AcquireLock(holder string, pid int) error
	ReleaseLock() error
	GetLock() (*store.Lock, error)
	CreateApply(id, specHash string) error
	GetApply(id string) (*store.Apply, error)
	CompleteApply(id string) error
	FailApply(id string) error
	ListApplies(limit int) ([]store.Apply, error)
	CreateApplyAction(id, applyID, resourceID, action string) error
	UpdateApplyAction(id, outcome, errMsg string) error
	ListApplyActions(applyID string) ([]store.ApplyAction, error)
	CreateGeneration(g store.Generation) error
	UpdateGeneration(id, outputText, outcome, rejectionReason string, durationMS, inputTokens, outputTokens int64, costUSD float64) error
	ListGenerations(resourceID string, limit int) ([]store.Generation, error)
	CreateSession(sess store.Session) error
	GetSession(id string) (*store.Session, error)
	GetActiveSession() (*store.Session, error)
	UpdateSession(id, status string, currentWave int) error
	SetNote(resourceID, applyID, content string) error
	GetNote(resourceID, applyID string) (string, error)
	ListNotes(applyID string) ([]store.AgentNote, error)
}

type Spec struct {
	engine specEngine
	store  specStore
	fs     fileSystem
	cfg    *config.Config
}

func New(eng specEngine, st specStore, fs fileSystem, cfg *config.Config) *Spec {
	return &Spec{
		engine: eng,
		store:  st,
		fs:     fs,
		cfg:    cfg,
	}
}

type PlanResult struct {
	Actions  []planpkg.PlannedAction
	Registry *cuepkg.Registry
	Graph    *graphpkg.Graph
	Waves    [][]string
	Hashes   map[string]string
}

func (s *Spec) Plan(ctx context.Context) (*PlanResult, error) {
	project, err := cuepkg.Load(s.cfg.SpecDir)
	if err != nil {
		return nil, err
	}

	registry, err := cuepkg.NewRegistry(project)
	if err != nil {
		return nil, err
	}

	g, err := graphpkg.Build(registry.Resources)
	if err != nil {
		return nil, err
	}

	model := s.cfg.GenerateModel
	hashes := graphpkg.ComputeEffectiveHashes(registry.Resources, g, model)

	planner := planpkg.New(s.store, s.fs)
	actions, err := planner.Plan(ctx, registry, g, model)
	if err != nil {
		return nil, err
	}

	waves, err := g.Waves()
	if err != nil {
		return nil, err
	}

	waveStrings := make([][]string, len(waves))
	for i, wave := range waves {
		waveStrings[i] = wave
	}

	return &PlanResult{
		Actions:  actions,
		Registry: registry,
		Graph:    g,
		Waves:    waveStrings,
		Hashes:   hashes,
	}, nil
}
```

- [ ] **Step 4: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/spec/ -v
```

Expected: All tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/spec/state.go internal/spec/state_test.go internal/spec/spec.go
git commit -m "feat(sp5): implement resource state machine and spec engine struct"
```

---

## Task 5: Runtime context builder

**Files:**
- Create: `internal/spec/runtime.go`
- Create: `internal/spec/runtime_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/spec/runtime_test.go`:

```go
package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildModuleTree(t *testing.T) {
	dir := t.TempDir()
	fs := OSFileSystem{}

	require.NoError(t, fs.MkdirAll(dir+"/src/Synth/Voice", 0o755))
	require.NoError(t, fs.WriteFile(dir+"/src/Synth/Voice/voice.rs", []byte("code"), 0o644))
	require.NoError(t, fs.WriteFile(dir+"/src/Synth/Voice/voice_test.rs", []byte("test"), 0o644))

	tree, err := buildModuleTree(fs, dir+"/src")
	require.NoError(t, err)
	assert.Contains(t, tree, "Synth")
	assert.Contains(t, tree, "Voice")
	assert.Contains(t, tree, "voice.rs")
}

func TestBuildModuleTree_EmptyDir(t *testing.T) {
	tree, err := buildModuleTree(OSFileSystem{}, "/nonexistent/path")
	require.NoError(t, err)
	assert.Equal(t, "", tree)
}
```

- [ ] **Step 2: Implement runtime.go**

Create `internal/spec/runtime.go`:

```go
package spec

import (
	"fmt"
	"path/filepath"
	"strings"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/prompt"
)

func (s *Spec) buildRuntimeContext(resource cuepkg.Resource, registry *cuepkg.Registry, applyID string) (prompt.RuntimeContext, error) {
	ctx := prompt.RuntimeContext{}

	srcDir := filepath.Join(filepath.Dir(s.cfg.SpecDir), "src")
	tree, err := buildModuleTree(s.fs, srcDir)
	if err == nil && tree != "" {
		ctx.ModuleTree = tree
	}

	depFiles := make(map[string]string)
	for _, dep := range resource.Dependencies {
		files, err := s.store.GetGeneratedFiles(dep.TargetID)
		if err != nil {
			continue
		}
		for _, f := range files {
			if strings.Contains(f.Path, "_test") || strings.Contains(f.Path, "test_") {
				continue
			}
			data, err := s.fs.ReadFile(f.Path)
			if err != nil {
				continue
			}
			depFiles[dep.TargetID] = string(data)
			break
		}
	}
	if len(depFiles) > 0 {
		ctx.DependencyFiles = depFiles
	}

	if applyID != "" {
		notes := make(map[string]string)
		for _, dep := range resource.Dependencies {
			content, err := s.store.GetNote(dep.TargetID, applyID)
			if err != nil || content == "" {
				continue
			}
			notes[dep.TargetID] = content
		}
		if len(notes) > 0 {
			ctx.AgentNotes = notes
		}
	}

	return ctx, nil
}

func buildModuleTree(fs fileSystem, dir string) (string, error) {
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return "", nil
	}

	var b strings.Builder
	buildTreeRecursive(fs, dir, "", &b, entries)
	return b.String(), nil
}

func buildTreeRecursive(fs fileSystem, basePath, prefix string, b *strings.Builder, entries []Entry) {
	for i, e := range entries {
		connector := "├── "
		if i == len(entries)-1 {
			connector = "└── "
		}
		b.WriteString(prefix + connector + e.Name() + "\n")

		if e.IsDir() {
			childPrefix := prefix + "│   "
			if i == len(entries)-1 {
				childPrefix = prefix + "    "
			}
			childEntries, err := fs.ReadDir(filepath.Join(basePath, e.Name()))
			if err != nil {
				continue
			}
			buildTreeRecursive(fs, filepath.Join(basePath, e.Name()), childPrefix, b, childEntries)
		}
	}
}

type Entry = interface {
	Name() string
	IsDir() bool
}
```

Note: The `Entry` type alias won't work as-is because `os.DirEntry` has more methods. The implementer should use `[]os.DirEntry` directly and adjust the type — the `fs.ReadDir` return type already provides `os.DirEntry`. Remove the `Entry` type alias and use `[]os.DirEntry` throughout:

```go
func buildTreeRecursive(fsys fileSystem, basePath, prefix string, b *strings.Builder, entries []os.DirEntry) {
```

And add `"os"` to imports.

- [ ] **Step 3: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/spec/ -run "TestBuildModuleTree" -v
```

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/spec/runtime.go internal/spec/runtime_test.go
git commit -m "feat(sp5): implement runtime context builder"
```

---

## Task 6: Constraint loop

**Files:**
- Create: `internal/spec/loop.go`
- Create: `internal/spec/loop_test.go`

- [ ] **Step 1: Write failing tests**

Create `internal/spec/loop_test.go`:

```go
package spec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/engine"
)

type mockEngine struct {
	generateFn func(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error)
	reviewFn   func(ctx context.Context, opts engine.ReviewOpts) (*agent.RunResult, error)
}

func (m *mockEngine) Generate(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error) {
	if m.generateFn != nil {
		return m.generateFn(ctx, opts)
	}
	return &agent.RunResult{Output: ""}, nil
}

func (m *mockEngine) Review(ctx context.Context, opts engine.ReviewOpts) (*agent.RunResult, error) {
	if m.reviewFn != nil {
		return m.reviewFn(ctx, opts)
	}
	return &agent.RunResult{Output: "PASS"}, nil
}

func (m *mockEngine) CodeReview(ctx context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error) {
	return &agent.RunResult{Output: "PASS: no issues found"}, nil
}

func (m *mockEngine) Bugbot(ctx context.Context, opts engine.BugbotOpts) (*agent.RunResult, error) {
	return &agent.RunResult{Output: "No bugs found"}, nil
}

func TestConstraintLoop_PassOnFirstTry(t *testing.T) {
	eng := &mockEngine{
		generateFn: func(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error) {
			return &agent.RunResult{
				Output: "```go\n// path: src/voice.go\npackage voice\n```\n",
			}, nil
		},
	}

	result, err := runConstraintLoop(context.Background(), eng, LoopOpts{
		Prompt:      "generate voice",
		Model:       "test-model",
		MaxRetries:  3,
		ReviewLevel: "skip",
	})

	require.NoError(t, err)
	assert.Equal(t, "accepted", result.Outcome)
	assert.Equal(t, 1, result.Attempts)
	require.Len(t, result.Files, 1)
	assert.Equal(t, "src/voice.go", result.Files[0].Path)
}

func TestConstraintLoop_RetryOnParseFailure(t *testing.T) {
	calls := 0
	eng := &mockEngine{
		generateFn: func(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error) {
			calls++
			if calls == 1 {
				return &agent.RunResult{Output: "I can't generate that"}, nil
			}
			return &agent.RunResult{
				Output: "```go\n// path: src/voice.go\npackage voice\n```\n",
			}, nil
		},
	}

	result, err := runConstraintLoop(context.Background(), eng, LoopOpts{
		Prompt:      "generate voice",
		Model:       "test-model",
		MaxRetries:  3,
		ReviewLevel: "skip",
	})

	require.NoError(t, err)
	assert.Equal(t, "accepted", result.Outcome)
	assert.Equal(t, 2, result.Attempts)
}

func TestConstraintLoop_ExhaustedRetries(t *testing.T) {
	eng := &mockEngine{
		generateFn: func(ctx context.Context, opts engine.GenerateOpts) (*agent.RunResult, error) {
			return &agent.RunResult{Output: "I can't do this"}, nil
		},
	}

	result, err := runConstraintLoop(context.Background(), eng, LoopOpts{
		Prompt:      "generate voice",
		Model:       "test-model",
		MaxRetries:  2,
		ReviewLevel: "skip",
	})

	require.NoError(t, err)
	assert.Equal(t, "rejected", result.Outcome)
	assert.Equal(t, 3, result.Attempts) // initial + 2 retries
}
```

- [ ] **Step 2: Implement loop.go**

Create `internal/spec/loop.go`:

```go
package spec

import (
	"context"
	"fmt"
	"strings"

	"github.com/crestenstclair/crest-spec/internal/engine"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
)

type LoopResult struct {
	Files           []CodeBlock
	Outcome         string
	RejectionReason string
	Attempts        int
}

type LoopOpts struct {
	SystemPrompt string
	Prompt       string
	Model        string
	MaxRetries   int
	ReviewLevel  string
	Cwd          string
}

func runConstraintLoop(ctx context.Context, eng specEngine, opts LoopOpts) (*LoopResult, error) {
	maxAttempts := opts.MaxRetries + 1
	currentPrompt := opts.Prompt
	var lastOutput string
	var lastError string

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		genPrompt := currentPrompt
		if attempt > 1 && lastError != "" {
			genPrompt = promptpkg.BuildFixPrompt(opts.Prompt, lastOutput, lastError)
		}

		res, err := eng.Generate(ctx, engine.GenerateOpts{
			Prompt:             genPrompt,
			Model:              opts.Model,
			AppendSystemPrompt: opts.SystemPrompt,
		})
		if err != nil {
			return nil, fmt.Errorf("generate attempt %d: %w", attempt, err)
		}

		lastOutput = res.Output

		blocks, parseErr := ParseCodeBlocks(res.Output)
		if parseErr != nil {
			lastError = fmt.Sprintf("parse error: %s", parseErr.Error())
			continue
		}

		if opts.ReviewLevel != "" && opts.ReviewLevel != "skip" {
			reviewResult, reviewErr := runReview(ctx, eng, res.Output, opts)
			if reviewErr != nil {
				return nil, fmt.Errorf("review attempt %d: %w", attempt, reviewErr)
			}
			if !reviewResult.Passed {
				lastError = fmt.Sprintf("review failed: %s", reviewResult.Message)
				continue
			}
		}

		return &LoopResult{
			Files:   blocks,
			Outcome: "accepted",
			Attempts: attempt,
		}, nil
	}

	return &LoopResult{
		Outcome:         "rejected",
		RejectionReason: lastError,
		Attempts:        maxAttempts,
	}, nil
}

func runReview(ctx context.Context, eng specEngine, code string, opts LoopOpts) (*ValidationResult, error) {
	switch opts.ReviewLevel {
	case "full":
		res, err := eng.CodeReview(ctx, engine.CodeReviewOpts{
			Prompt: fmt.Sprintf("Review this generated code:\n\n%s", code),
			Cwd:    opts.Cwd,
		})
		if err != nil {
			return nil, err
		}
		passed := !strings.Contains(strings.ToUpper(res.Output), "FAIL")
		return &ValidationResult{Passed: passed, Kind: "review", Message: res.Output}, nil

	case "light":
		res, err := eng.Bugbot(ctx, engine.BugbotOpts{
			Prompt: code,
			Cwd:    opts.Cwd,
		})
		if err != nil {
			return nil, err
		}
		passed := !strings.Contains(strings.ToLower(res.Output), "critical")
		return &ValidationResult{Passed: passed, Kind: "review", Message: res.Output}, nil

	case "solid":
		res, err := eng.Review(ctx, engine.ReviewOpts{
			Code:         code,
			Requirements: opts.Prompt,
		})
		if err != nil {
			return nil, err
		}
		passed := strings.Contains(strings.ToUpper(res.Output), "PASS")
		return &ValidationResult{Passed: passed, Kind: "review", Message: res.Output}, nil

	default:
		return &ValidationResult{Passed: true, Kind: "review"}, nil
	}
}
```

- [ ] **Step 3: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/spec/ -v
```

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/spec/loop.go internal/spec/loop_test.go
git commit -m "feat(sp5): implement constraint loop"
```

---

## Task 7: Interactive session (begin/next/context/commit/finish)

**Files:**
- Create: `internal/spec/session.go`
- Create: `internal/spec/session_test.go`

- [ ] **Step 1: Write session.go**

Create `internal/spec/session.go`:

```go
package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"
	"github.com/crestenstclair/crest-spec/internal/store"
)

type BeginOpts struct {
	Target string
	Force  bool
	Model  string
}

type BeginResult struct {
	SessionID    string
	ApplyID      string
	Plan         []planpkg.PlannedAction
	Waves        [][]string
	Instructions string
	DriftActions []planpkg.PlannedAction
}

type NextResult struct {
	Done      bool
	WaveIndex int
	Resources []ResourceStatus
}

type ContextResult struct {
	SystemPrompt    string
	Prompt          string
	DependencyNotes map[string]string
	Instructions    string
}

type CommitFile struct {
	Path    string
	Content string
}

type FinishResult struct {
	Committed int
	Skipped   int
	Errored   int
}

func (s *Spec) Begin(ctx context.Context, opts BeginOpts) (*BeginResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	if len(planResult.Actions) == 0 {
		return &BeginResult{
			Instructions: "No changes detected. The spec is up to date.",
		}, nil
	}

	applyID := uuid.NewString()
	sessionID := uuid.NewString()

	specJSON, _ := json.Marshal(planResult.Actions)
	specHash := fmt.Sprintf("%x", sha256.Sum256(specJSON))

	if err := s.store.CreateApply(applyID, specHash); err != nil {
		return nil, fmt.Errorf("create apply: %w", err)
	}

	if err := s.store.AcquireLock(sessionID, os.Getpid()); err != nil {
		return nil, fmt.Errorf("acquire lock: %w", err)
	}

	planJSON, _ := json.Marshal(planResult.Actions)
	wavesJSON, _ := json.Marshal(planResult.Waves)
	hashesJSON, _ := json.Marshal(planResult.Hashes)

	if err := s.store.CreateSession(store.Session{
		ID:         sessionID,
		PlanJSON:   string(planJSON),
		WavesJSON:  string(wavesJSON),
		HashesJSON: string(hashesJSON),
	}); err != nil {
		s.store.ReleaseLock()
		return nil, fmt.Errorf("create session: %w", err)
	}

	var driftActions []planpkg.PlannedAction
	var otherActions []planpkg.PlannedAction
	for _, a := range planResult.Actions {
		if a.Kind == planpkg.ActionDrift {
			driftActions = append(driftActions, a)
		} else {
			otherActions = append(otherActions, a)
		}
	}

	instructions := `You are a dispatcher, not a code generator. Do not write code yourself.
For each resource: call spec/context to get its prompt, then call run_prompt with that prompt (using --disallowedTools for constrained output), parse the output, write files, call spec/note with design decisions, call spec/commit.
Use poll_result to collect run_prompt results (they're async).
Resources within the same wave can be dispatched in parallel (multiple run_prompt calls).
Waves must be processed sequentially.`

	return &BeginResult{
		SessionID:    sessionID,
		ApplyID:      applyID,
		Plan:         otherActions,
		Waves:        planResult.Waves,
		Instructions: instructions,
		DriftActions: driftActions,
	}, nil
}

func (s *Spec) Next(ctx context.Context, sessionID string) (*NextResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	var waves [][]string
	if err := json.Unmarshal([]byte(sess.WavesJSON), &waves); err != nil {
		return nil, fmt.Errorf("unmarshal waves: %w", err)
	}

	if sess.CurrentWave >= len(waves) {
		return &NextResult{Done: true, WaveIndex: sess.CurrentWave}, nil
	}

	wave := waves[sess.CurrentWave]

	var plan []planpkg.PlannedAction
	if err := json.Unmarshal([]byte(sess.PlanJSON), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}

	planSet := make(map[string]bool)
	for _, a := range plan {
		planSet[a.ResourceID] = true
	}

	var resources []ResourceStatus
	for _, id := range wave {
		if !planSet[id] {
			continue
		}
		resources = append(resources, ResourceStatus{
			ResourceID: id,
			State:      StatePending,
			WaveIndex:  sess.CurrentWave,
			MaxRetries: s.cfg.MaxRetries,
		})
	}

	if len(resources) == 0 {
		if err := s.store.UpdateSession(sessionID, sess.Status, sess.CurrentWave+1); err != nil {
			return nil, fmt.Errorf("advance wave: %w", err)
		}
		return s.Next(ctx, sessionID)
	}

	return &NextResult{
		Done:      false,
		WaveIndex: sess.CurrentWave,
		Resources: resources,
	}, nil
}

func (s *Spec) Context(ctx context.Context, sessionID, resourceID string) (*ContextResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	var plan []planpkg.PlannedAction
	if err := json.Unmarshal([]byte(sess.PlanJSON), &plan); err != nil {
		return nil, fmt.Errorf("unmarshal plan: %w", err)
	}

	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan: %w", err)
	}

	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}

	systemPrompt := promptpkg.BuildSystemPrompt(planResult.Registry.Project)
	resourcePrompt := promptpkg.BuildResourcePrompt(resource, planResult.Registry)

	runtimeCtx, _ := s.buildRuntimeContext(resource, planResult.Registry, "")
	fullPrompt := promptpkg.InjectRuntimeContext(resourcePrompt, runtimeCtx)

	var depNotes map[string]string
	if len(resource.Dependencies) > 0 {
		depNotes = make(map[string]string)
		for _, dep := range resource.Dependencies {
			content, err := s.store.GetNote(dep.TargetID, "")
			if err == nil && content != "" {
				depNotes[dep.TargetID] = content
			}
		}
	}

	return &ContextResult{
		SystemPrompt:    systemPrompt,
		Prompt:          fullPrompt,
		DependencyNotes: depNotes,
		Instructions:    fmt.Sprintf("Generate code for resource %s. Use --disallowedTools to prevent tool access. Return pure code blocks with // path: annotations.", resourceID),
	}, nil
}

func (s *Spec) Commit(ctx context.Context, sessionID, resourceID string, files []CommitFile, notes string) error {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	for _, f := range files {
		dir := filepath.Dir(f.Path)
		if err := s.fs.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}

		contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(f.Content)))

		existing, readErr := s.fs.ReadFile(f.Path)
		if readErr == nil {
			existingHash := fmt.Sprintf("%x", sha256.Sum256(existing))
			if existingHash == contentHash {
				continue
			}
		}

		if err := s.fs.WriteFile(f.Path, []byte(f.Content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", f.Path, err)
		}
	}

	var plan []planpkg.PlannedAction
	json.Unmarshal([]byte(sess.PlanJSON), &plan)

	var hashes map[string]string
	json.Unmarshal([]byte(sess.HashesJSON), &hashes)

	var action planpkg.PlannedAction
	for _, a := range plan {
		if a.ResourceID == resourceID {
			action = a
			break
		}
	}

	planResult, err := s.Plan(ctx)
	if err != nil {
		return fmt.Errorf("plan for commit: %w", err)
	}

	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return fmt.Errorf("resource not found: %s", resourceID)
	}

	declHash := fmt.Sprintf("%x", sha256.Sum256(func() []byte { b, _ := json.Marshal(resource.Declaration); return b }()))

	if err := s.store.SetResource(store.Resource{
		ID:              resourceID,
		Kind:            resource.Kind,
		ContextName:     resource.ContextName,
		DeclarationHash: declHash,
		EffectiveHash:   hashes[resourceID],
		Model:           s.cfg.GenerateModel,
		SettledAt:       time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("set resource: %w", err)
	}

	s.store.DeleteGeneratedFiles(resourceID)
	for _, f := range files {
		contentHash := fmt.Sprintf("%x", sha256.Sum256([]byte(f.Content)))
		promptHash := ""
		s.store.SetGeneratedFile(store.GeneratedFile{
			Path:        f.Path,
			ResourceID:  resourceID,
			ContentHash: contentHash,
			PromptHash:  promptHash,
			Model:       s.cfg.GenerateModel,
			CreatedAt:   time.Now().UTC(),
		})
	}

	s.store.DeleteDependencies(resourceID)
	for _, dep := range resource.Dependencies {
		s.store.SetDependency(resourceID, dep.TargetID, dep.Kind)
	}

	if notes != "" {
		applyID := ""
		for _, a := range plan {
			if a.ResourceID == resourceID {
				break
			}
		}
		_ = action
		s.store.SetNote(resourceID, applyID, notes)
	}

	return nil
}

func (s *Spec) Finish(ctx context.Context, sessionID string, force bool) (*FinishResult, error) {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return nil, fmt.Errorf("get session: %w", err)
	}

	if err := s.store.UpdateSession(sessionID, "completed", sess.CurrentWave); err != nil {
		return nil, fmt.Errorf("update session: %w", err)
	}

	if err := s.store.ReleaseLock(); err != nil {
		return nil, fmt.Errorf("release lock: %w", err)
	}

	return &FinishResult{}, nil
}

func (s *Spec) AdvanceWave(ctx context.Context, sessionID string) error {
	sess, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	return s.store.UpdateSession(sessionID, sess.Status, sess.CurrentWave+1)
}
```

- [ ] **Step 2: Write session_test.go**

Create `internal/spec/session_test.go`:

```go
package spec

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBeginResult_HasInstructions(t *testing.T) {
	result := &BeginResult{
		SessionID:    "sess-1",
		Instructions: "You are a dispatcher",
	}
	assert.Contains(t, result.Instructions, "dispatcher")
}

func TestNextResult_Done(t *testing.T) {
	result := &NextResult{Done: true, WaveIndex: 3}
	assert.True(t, result.Done)
}

func TestContextResult_HasPrompts(t *testing.T) {
	result := &ContextResult{
		SystemPrompt: "You are a go code generator",
		Prompt:       "# Resource: aggregate",
	}
	assert.NotEmpty(t, result.SystemPrompt)
	assert.NotEmpty(t, result.Prompt)
}
```

- [ ] **Step 3: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/spec/ -v
```

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/spec/session.go internal/spec/session_test.go
git commit -m "feat(sp5): implement interactive session management"
```

---

## Task 8: Resolution and query operations

**Files:**
- Create: `internal/spec/resolve.go`
- Create: `internal/spec/query.go`

- [ ] **Step 1: Implement resolve.go**

Create `internal/spec/resolve.go`:

```go
package spec

import (
	"context"
	"fmt"
)

func (s *Spec) Resolve(ctx context.Context, sessionID, resourceID, answer string, model string) error {
	_, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	if err := s.store.SetNote(resourceID, sessionID, answer); err != nil {
		return fmt.Errorf("set note: %w", err)
	}

	return nil
}

func (s *Spec) Amend(ctx context.Context, sessionID, resourceID string) error {
	_, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	_, planErr := s.Plan(ctx)
	if planErr != nil {
		return fmt.Errorf("re-plan after amend: %w", planErr)
	}

	return nil
}

func (s *Spec) Skip(ctx context.Context, sessionID, resourceID, reason string) error {
	_, err := s.store.GetSession(sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	if reason != "" {
		s.store.SetNote(resourceID, sessionID, fmt.Sprintf("SKIPPED: %s", reason))
	}

	return nil
}
```

- [ ] **Step 2: Implement query.go**

Create `internal/spec/query.go`:

```go
package spec

import (
	"context"
	"fmt"

	"github.com/crestenstclair/crest-spec/internal/store"
)

type StatusResult struct {
	Resources    []store.Resource
	ActiveLock   *store.Lock
	Session      *store.Session
}

func (s *Spec) Status(ctx context.Context) (*StatusResult, error) {
	resources, err := s.store.ListResources()
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}

	lock, _ := s.store.GetLock()
	session, _ := s.store.GetActiveSession()

	return &StatusResult{
		Resources:  resources,
		ActiveLock: lock,
		Session:    session,
	}, nil
}

func (s *Spec) Log(ctx context.Context, limit int) ([]store.Apply, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.store.ListApplies(limit)
}

func (s *Spec) History(ctx context.Context, resourceID string, limit int) ([]store.Generation, error) {
	if limit <= 0 {
		limit = 20
	}
	return s.store.ListGenerations(resourceID, limit)
}

type GraphResult struct {
	Nodes []string
	Waves [][]string
}

func (s *Spec) GraphInfo(ctx context.Context) (*GraphResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, err
	}

	topo, err := planResult.Graph.TopologicalSort()
	if err != nil {
		return nil, err
	}

	return &GraphResult{
		Nodes: topo,
		Waves: planResult.Waves,
	}, nil
}

func (s *Spec) Unlock(ctx context.Context) error {
	return s.store.ReleaseLock()
}

func (s *Spec) DriftAction(ctx context.Context, action, resourceID string) error {
	switch action {
	case "accept":
		files, err := s.store.GetGeneratedFiles(resourceID)
		if err != nil {
			return fmt.Errorf("get files: %w", err)
		}
		for _, f := range files {
			data, err := s.fs.ReadFile(f.Path)
			if err != nil {
				continue
			}
			contentHash := fmt.Sprintf("%x", sha256Hash(data))
			s.store.SetGeneratedFile(store.GeneratedFile{
				Path:        f.Path,
				ResourceID:  f.ResourceID,
				ContentHash: contentHash,
				PromptHash:  f.PromptHash,
				Model:       f.Model,
				CreatedAt:   f.CreatedAt,
			})
		}
		return nil

	case "revert":
		files, err := s.store.GetGeneratedFiles(resourceID)
		if err != nil {
			return fmt.Errorf("get files: %w", err)
		}
		_ = files
		return fmt.Errorf("revert not yet implemented: need stored file content")

	default:
		return fmt.Errorf("unknown drift action: %s (expected 'accept' or 'revert')", action)
	}
}

func (s *Spec) Validate(ctx context.Context) (*ValidateResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, err
	}

	return &ValidateResult{
		Valid:         true,
		ResourceCount: len(planResult.Registry.Resources),
	}, nil
}

type ValidateResult struct {
	Valid          bool
	ResourceCount int
	Errors        []string
}

func (s *Spec) ValidateResource(ctx context.Context, resourceID string) (*ValidateResourceResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, err
	}

	resource, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}

	var results []ValidationResult
	if len(resource.Validations) > 0 {
		cwd := "."
		results, err = RunValidations(ctx, resource.Validations, cwd)
		if err != nil {
			return nil, err
		}
	}

	return &ValidateResourceResult{
		ResourceID:  resourceID,
		Validations: results,
	}, nil
}

type ValidateResourceResult struct {
	ResourceID  string
	Validations []ValidationResult
}
```

Add a helper for sha256 hashing at the bottom of `query.go`:

```go
import "crypto/sha256"

func sha256Hash(data []byte) [32]byte {
	return sha256.Sum256(data)
}
```

- [ ] **Step 3: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./internal/spec/ -v
```

Expected: All tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/spec/resolve.go internal/spec/query.go
git commit -m "feat(sp5): implement resolution and query operations"
```

---

## Task 9: MCP tool handlers — replace spec stubs

**Files:**
- Modify: `internal/mcp/server.go`
- Modify: `internal/mcp/tools.go`

- [ ] **Step 1: Update server.go to accept Spec**

In `internal/mcp/server.go`, add the spec import and field:

Add to Server struct:
```go
spec    specHandler
```

Add interface before Server struct:
```go
type specHandler interface {
	Plan(ctx context.Context) (*specmod.PlanResult, error)
	Begin(ctx context.Context, opts specmod.BeginOpts) (*specmod.BeginResult, error)
	Next(ctx context.Context, sessionID string) (*specmod.NextResult, error)
	Context(ctx context.Context, sessionID, resourceID string) (*specmod.ContextResult, error)
	Commit(ctx context.Context, sessionID, resourceID string, files []specmod.CommitFile, notes string) error
	Finish(ctx context.Context, sessionID string, force bool) (*specmod.FinishResult, error)
	AdvanceWave(ctx context.Context, sessionID string) error
	Resolve(ctx context.Context, sessionID, resourceID, answer string, model string) error
	Amend(ctx context.Context, sessionID, resourceID string) error
	Skip(ctx context.Context, sessionID, resourceID, reason string) error
	Status(ctx context.Context) (*specmod.StatusResult, error)
	Log(ctx context.Context, limit int) ([]storemod.Apply, error)
	History(ctx context.Context, resourceID string, limit int) ([]storemod.Generation, error)
	GraphInfo(ctx context.Context) (*specmod.GraphResult, error)
	Validate(ctx context.Context) (*specmod.ValidateResult, error)
	ValidateResource(ctx context.Context, resourceID string) (*specmod.ValidateResourceResult, error)
	DriftAction(ctx context.Context, action, resourceID string) error
	Unlock(ctx context.Context) error
}
```

Update `New()` signature to accept spec:
```go
func New(
	spec specHandler,
	eng engine,
	st store,
	pt processTree,
	stdin io.Reader,
	stdout io.Writer,
	log zerolog.Logger,
	cfg *config.Config,
) *Server {
```

And set `s.spec = spec` in the constructor body.

- [ ] **Step 2: Replace spec tool stubs in tools.go**

In `internal/mcp/tools.go`, replace the `specStubs` loop (lines ~237–269) with real handlers that delegate to `s.spec`. Each handler:
1. Unmarshals its arguments from `args json.RawMessage`
2. Calls the corresponding `s.spec.Method(ctx, ...)`
3. Returns `jsonResult(result)` or `errorResult(err.Error())`

Replace the entire `specStubs` section with individual `s.addTool()` calls. Here are the key ones (the implementer should add all 23):

```go
// spec/plan
s.addTool(toolDef{
    Name: "spec/plan",
    Description: "Show what would change (dry run)",
    InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
    result, err := s.spec.Plan(ctx)
    if err != nil {
        return errorResult(fmt.Sprintf("plan: %v", err))
    }
    return jsonResult(result.Actions)
})

// spec/begin
s.addTool(toolDef{
    Name: "spec/begin",
    Description: "Start interactive agent session",
    InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"force":{"type":"boolean"},"model":{"type":"string"}}}`),
}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
    var p struct {
        Target string `json:"target"`
        Force  bool   `json:"force"`
        Model  string `json:"model"`
    }
    json.Unmarshal(args, &p)
    result, err := s.spec.Begin(ctx, specmod.BeginOpts{Target: p.Target, Force: p.Force, Model: p.Model})
    if err != nil {
        return errorResult(fmt.Sprintf("begin: %v", err))
    }
    return jsonResult(result)
})

// spec/next
s.addTool(toolDef{
    Name: "spec/next",
    Description: "Get next wave of uncommitted resources",
    InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string","description":"Session ID"}},"required":["session_id"]}`),
}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
    var p struct{ SessionID string `json:"session_id"` }
    json.Unmarshal(args, &p)
    result, err := s.spec.Next(ctx, p.SessionID)
    if err != nil {
        return errorResult(fmt.Sprintf("next: %v", err))
    }
    return jsonResult(result)
})

// spec/context
s.addTool(toolDef{
    Name: "spec/context",
    Description: "Get scoped prompt for a resource",
    InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"resource_id":{"type":"string"}},"required":["session_id","resource_id"]}`),
}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
    var p struct {
        SessionID  string `json:"session_id"`
        ResourceID string `json:"resource_id"`
    }
    json.Unmarshal(args, &p)
    result, err := s.spec.Context(ctx, p.SessionID, p.ResourceID)
    if err != nil {
        return errorResult(fmt.Sprintf("context: %v", err))
    }
    return jsonResult(result)
})

// spec/commit
s.addTool(toolDef{
    Name: "spec/commit",
    Description: "Record a resource as complete",
    InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"resource_id":{"type":"string"},"files":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}},"notes":{"type":"string"}},"required":["session_id","resource_id"]}`),
}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
    var p struct {
        SessionID  string `json:"session_id"`
        ResourceID string `json:"resource_id"`
        Files      []struct {
            Path    string `json:"path"`
            Content string `json:"content"`
        } `json:"files"`
        Notes string `json:"notes"`
    }
    json.Unmarshal(args, &p)
    var files []specmod.CommitFile
    for _, f := range p.Files {
        files = append(files, specmod.CommitFile{Path: f.Path, Content: f.Content})
    }
    if err := s.spec.Commit(ctx, p.SessionID, p.ResourceID, files, p.Notes); err != nil {
        return errorResult(fmt.Sprintf("commit: %v", err))
    }
    return jsonResult(map[string]bool{"committed": true})
})

// spec/finish
s.addTool(toolDef{
    Name: "spec/finish",
    Description: "Finalize session, release lock",
    InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"force":{"type":"boolean"}},"required":["session_id"]}`),
}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
    var p struct {
        SessionID string `json:"session_id"`
        Force     bool   `json:"force"`
    }
    json.Unmarshal(args, &p)
    result, err := s.spec.Finish(ctx, p.SessionID, p.Force)
    if err != nil {
        return errorResult(fmt.Sprintf("finish: %v", err))
    }
    return jsonResult(result)
})

// Continue this pattern for all remaining tools:
// spec/apply (async via runAsync), spec/validate, spec/validate-resource,
// spec/note, spec/resolve, spec/amend, spec/skip, spec/status, spec/log,
// spec/history, spec/graph, spec/diff, spec/state, spec/drift, spec/vacuum,
// spec/sql, spec/unlock
```

For `spec/apply`, use `s.runAsync`:
```go
s.addTool(toolDef{
    Name: "spec/apply",
    Description: "Execute the plan (async)",
    InputSchema: json.RawMessage(`{"type":"object","properties":{"target":{"type":"string"},"force":{"type":"boolean"}}}`),
}, func(ctx context.Context, args json.RawMessage, progressToken string) toolResult {
    return s.runAsync("spec/apply", func(ctx context.Context) (string, error) {
        result, err := s.spec.Begin(ctx, specmod.BeginOpts{})
        if err != nil {
            return "", err
        }
        b, _ := json.Marshal(result)
        return string(b), nil
    }, progressToken)
})
```

- [ ] **Step 3: Update main.go wiring**

In `cmd/crest-spec/main.go`:

Add import:
```go
specmod "github.com/crestenstclair/crest-spec/internal/spec"
```

After engine creation, create spec:
```go
sp := specmod.New(eng, s, specmod.OSFileSystem{}, cfg)
```

Update MCP server creation:
```go
srv := mcp.New(sp, eng, s, mcp.OSProcessTree{}, os.Stdin, os.Stdout, log.Logger, cfg)
```

- [ ] **Step 4: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./...
```

Expected: All tests pass. The existing MCP tests should still pass with the updated constructor.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/server.go internal/mcp/tools.go cmd/crest-spec/main.go
git commit -m "feat(sp5): wire spec engine into MCP tool handlers"
```

---

## Task 10: MCP resources and prompts handlers

**Files:**
- Modify: `internal/mcp/handlers.go`

- [ ] **Step 1: Implement resources and prompts handlers**

Update `internal/mcp/handlers.go` to replace the stub implementations:

```go
func (s *Server) handleResourcesList(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	resources := []map[string]string{
		{"uri": "crest-spec://plan", "name": "Current Plan", "mimeType": "application/json"},
		{"uri": "crest-spec://state", "name": "Spec State", "mimeType": "application/json"},
		{"uri": "crest-spec://graph", "name": "Dependency Graph", "mimeType": "application/json"},
		{"uri": "crest-spec://session", "name": "Active Session", "mimeType": "application/json"},
		{"uri": "crest-spec://metrics", "name": "Server Metrics", "mimeType": "application/json"},
	}
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  map[string]any{"resources": resources},
	}
}

func (s *Server) handleResourcesRead(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: err.Error()}}
	}

	var content any
	var readErr error

	switch p.URI {
	case "crest-spec://plan":
		result, err := s.spec.Plan(ctx)
		if err != nil {
			readErr = err
		} else {
			content = result.Actions
		}
	case "crest-spec://state":
		result, err := s.spec.Status(ctx)
		if err != nil {
			readErr = err
		} else {
			content = result
		}
	case "crest-spec://graph":
		result, err := s.spec.GraphInfo(ctx)
		if err != nil {
			readErr = err
		} else {
			content = result
		}
	case "crest-spec://metrics":
		content = s.metrics.Snapshot()
	default:
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "unknown resource: " + p.URI}}
	}

	if readErr != nil {
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32603, Message: readErr.Error()}}
	}

	b, _ := json.Marshal(content)
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"contents": []map[string]any{
				{"uri": p.URI, "mimeType": "application/json", "text": string(b)},
			},
		},
	}
}

func (s *Server) handlePromptsList(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	prompts := []map[string]any{
		{"name": "system_prompt", "description": "The system prompt for sub-agents"},
		{"name": "resource_prompt", "description": "Full resource prompt for a specific resource", "arguments": []map[string]string{{"name": "resource_id", "description": "Resource identifier", "required": "true"}}},
		{"name": "orchestrator_instructions", "description": "Orchestrator protocol instructions"},
	}
	return jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{"prompts": prompts}}
}

func (s *Server) handlePromptsGet(ctx context.Context, id any, params json.RawMessage) jsonRPCResponse {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: err.Error()}}
	}

	switch p.Name {
	case "system_prompt":
		result, err := s.spec.Plan(ctx)
		if err != nil {
			return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32603, Message: err.Error()}}
		}
		prompt := promptpkg.BuildSystemPrompt(result.Registry.Project)
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
			"messages": []map[string]string{{"role": "user", "content": prompt}},
		}}

	case "orchestrator_instructions":
		instructions := "You are a dispatcher, not a code generator. Do not write code yourself.\nFor each resource: call spec/context to get its prompt, then call run_prompt with that prompt."
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
			"messages": []map[string]string{{"role": "user", "content": instructions}},
		}}

	default:
		return jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "unknown prompt: " + p.Name}}
	}
}
```

Note: Add `promptpkg "github.com/crestenstclair/crest-spec/internal/prompt"` to imports.

- [ ] **Step 2: Run tests**

```bash
cd /Users/crestenstclair/workspace/claude-mcp-server && go test ./...
```

Expected: All tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/handlers.go
git commit -m "feat(sp5): implement MCP resources and prompts handlers"
```

---

## Summary

| Task | What it does |
|------|-------------|
| 1 | Store layer: SQL queries + sqlc + store methods for applies, generations, sessions, notes |
| 2 | Code block parser: extract fenced blocks with path annotations from LLM output |
| 3 | Validation runner + filesystem abstraction: run subprocess commands, check assertions |
| 4 | Resource state machine + spec engine struct with Plan operation |
| 5 | Runtime context builder: module tree, dependency files, agent notes |
| 6 | Constraint loop: generate → parse → review → retry |
| 7 | Interactive session: begin/next/context/commit/finish |
| 8 | Resolution (resolve/amend/skip) + query operations (status/log/history/graph) |
| 9 | MCP tool handlers: replace 23 stubs, wire spec into server, update main.go |
| 10 | MCP resources + prompts handlers |
