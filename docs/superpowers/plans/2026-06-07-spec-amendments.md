# Spec Amendments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a first-class `amendment` concept that turns review findings (and other targeted change requests) into durable, spec-resident, incrementally-applied modifications that flow through the normal generation loop.

**Architecture:** Amendments are resource-scoped metadata living in the CUE spec (the source of truth). Adding one changes the resource's declaration hash → the planner emits `ActionModify` → the resource regenerates in a new generic **UPDATE mode** (existing files + a flagged "CHANGES TO MAKE" block → minimal diff). A SQLite `amendments` table materializes lifecycle state (PENDING → APPLIED → VERIFIED → GRADUATED), reconciled from the spec vs. the last-committed spec hash — never an independent authority. Four human-gated MCP tools (`propose_amendments`, `apply_amendments`, `list_amendments`, `graduate_amendment`) drive the workflow. **This plan does NOT couple to the file-hash `DriftActions`/`spec_drift` machinery** (being removed per `docs/drop-drift-detection.md`) — amendments ride the spec-hash → `ActionModify` path.

**Tech Stack:** Go, CUE (`cuelang.org/go`), SQLite (`modernc.org/sqlite`), sqlc, embedded markdown prompt templates, MCP tool layer.

---

## Design decisions locked in (read before starting)

These resolve ambiguities so there are no judgment calls mid-implementation:

1. **Amendments attach to value-object, aggregate, entity, asset, adapter, port, repository, and service declaration structs** via an `Amendments []Amendment` field. A generic helper `ResourceAmendments(Resource) []Amendment` type-switches `Declaration` so the rest of the code is declaration-shape-agnostic.
2. **Hashing is automatic.** `declHash`/`ComputeEffectiveHashes` already `json.Marshal(r.Declaration)`. Once `Amendments` is a JSON field on the declaration struct, adding an amendment changes the hash → `ActionModify`. No planner change needed; we only add tests proving it.
3. **Write-back target:** approved amendments are written to a phase-override file `phases/phase-<N>.override-<ResourceName>.cue` (or, when not phase-structured, `spec/override-<ResourceName>.cue`), matching the existing override convention, with a `// Amendment (…)` WHY comment. The writer **merges** into an existing override file rather than clobbering it.
4. **UPDATE mode is generic.** It triggers whenever a resource being dispatched already has committed generated files (`len(GetGeneratedFiles) > 0`). Amendments are its first consumer; ordinary spec edits are another. Implemented in `buildRuntimeContext` + `InjectRuntimeContext`, prompt text in an embedded markdown template.
5. **`applied` is derived.** The amendments table is rewritten by a reconciliation routine that compares each amendment's presence in the current spec against the resource's last-committed declaration hash. The table is a cache/index, never the source of truth.
6. **Amendment identity** = `content_hash` = sha256 of `{name, prompt, finding}`. Same content ⇒ same identity ⇒ idempotent reconciliation.
7. **`apply=false` previews, `apply=true` writes** — exactly mirroring `PromoteLearnings`. Nothing mutates source on a preview.

---

## File structure

**Create:**
- `migrations/013_amendments.sql` — amendments table DDL
- `sql/queries/amendments.sql` — sqlc CRUD queries
- `internal/spec/amendments.go` — `Amend`-adjacent: propose/apply/list/graduate + reconciliation
- `internal/spec/amendments_test.go` — unit tests for the above
- `internal/spec/cuewrite.go` — programmatic CUE override write-back + diff renderer
- `internal/spec/cuewrite_test.go`
- `internal/prompt/templates/update.md` — UPDATE-mode "CHANGES TO MAKE" framing
- `internal/prompt/templates/propose_amendments.md` — LLM drafting prompt for proposer

**Modify:**
- `internal/cue/types.go` — add `Amendment`, `Finding` structs; add `Amendments` field to declaration structs
- `internal/cue/registry.go` — add `ResourceAmendments(Resource) []Amendment` helper
- `internal/plan/planner_test.go` — add amendment-triggers-ActionModify test
- `internal/store/store.go` — `Amendment` domain type, converter, CRUD wrappers
- `internal/prompt/context.go` — `RuntimeContext.ExistingFiles` + `.ChangesRequired`; render UPDATE sections
- `internal/prompt/system.go` — (no change needed; UPDATE framing lives in context.go injection)
- `internal/spec/runtime.go` — populate `ExistingFiles`/`ChangesRequired` when resource has committed output + pending amendments
- `internal/spec/review.go` — expose findings for proposer (reuse `ReviewFinding`)
- `internal/mcp/server.go` — extend `specHandler` interface
- `internal/mcp/tools.go` — register 4 new tools + arg structs
- `SPEC.md` — document the amendments workflow + tools

---

## Phase 1 — CUE model + hashing (adding an amendment triggers regeneration)

### Task 1: Amendment + Finding Go types

**Files:**
- Modify: `internal/cue/types.go`
- Test: `internal/cue/amendment_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/cue/amendment_test.go`:

```go
package cue

import (
	"encoding/json"
	"testing"
)

func TestAmendment_JSONRoundTrip(t *testing.T) {
	a := Amendment{
		Name:   "validate-reference-pitch",
		Prompt: "EqualTemperament::new must reject 0.0, negative, NaN, and ∞ reference pitches.",
		Origin: "deep_review",
		Finding: &Finding{
			Severity: "major",
			File:     "src/audio/equal_temperament.rs",
			Line:     17,
			Text:     "accepts invalid reference pitches with no validation",
		},
		CreatedAt: "2026-06-07T00:00:00Z",
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Amendment
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Name != a.Name || got.Finding == nil || got.Finding.Line != 17 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cue/ -run TestAmendment_JSONRoundTrip -v`
Expected: FAIL — `undefined: Amendment`

- [ ] **Step 3: Add the types**

In `internal/cue/types.go`, after the `Validation`/`Assertion` block, add:

```go
// Amendment is a resource-scoped, spec-resident correction (e.g. distilled from
// a deep_review finding). Adding one to a resource's declaration changes its
// hash, which makes the planner re-generate that resource in UPDATE mode.
type Amendment struct {
	Name       string      `json:"name"`              // stable kebab-case id within the resource
	Prompt     string      `json:"prompt"`            // the targeted change instruction (data)
	Origin     string      `json:"origin,omitempty"`  // "deep_review" | "manual" | "bugbot" | ...
	Finding    *Finding    `json:"finding,omitempty"` // provenance
	Validation *Validation `json:"validation,omitempty"`
	Graduated  bool        `json:"graduated,omitempty"`
	CreatedAt  string      `json:"createdAt,omitempty"`
}

// Finding is the provenance of an amendment drawn from a review.
type Finding struct {
	Severity string `json:"severity,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Text     string `json:"text,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cue/ -run TestAmendment_JSONRoundTrip -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cue/types.go internal/cue/amendment_test.go
git commit -m "feat(cue): add Amendment and Finding types"
```

### Task 2: Thread `amendments` onto declaration structs

**Files:**
- Modify: `internal/cue/types.go` (Aggregate, ValueObject, Entity, Asset, Adapter, and any Port/Repository/DomainService/ApplicationService structs that exist)
- Test: `internal/cue/amendment_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cue/amendment_test.go`:

```go
func TestValueObject_CarriesAmendments(t *testing.T) {
	raw := `{
		"from": "f64",
		"description": "equal temperament tuning",
		"invariants": ["reference pitch must be finite and positive"],
		"amendments": [
			{"name": "validate-reference-pitch", "prompt": "reject 0.0/NaN/inf", "origin": "deep_review"}
		]
	}`
	var vo ValueObject
	if err := json.Unmarshal([]byte(raw), &vo); err != nil {
		t.Fatalf("unmarshal value object: %v", err)
	}
	if len(vo.Amendments) != 1 || vo.Amendments[0].Name != "validate-reference-pitch" {
		t.Fatalf("amendments not threaded: %+v", vo.Amendments)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cue/ -run TestValueObject_CarriesAmendments -v`
Expected: FAIL — `vo.Amendments undefined`

- [ ] **Step 3: Add the field to declaration structs**

In `internal/cue/types.go`, add `Amendments []Amendment \`json:"amendments,omitempty"\`` to each of: `Aggregate`, `ValueObject`, `Entity`, `Asset`, `Adapter`. (Grep first: `grep -n "Invariants  \[\]string\|Validations \[\]Validation" internal/cue/types.go` to find every declaration struct that already carries `Invariants`/`Validations`; add `Amendments` next to `Validations` in each.) Example for `ValueObject`:

```go
type ValueObject struct {
	From        string            `json:"from,omitempty"`
	State       map[string]string `json:"state,omitempty"`
	Description string            `json:"description,omitempty"`
	Invariants  []string          `json:"invariants,omitempty"`
	Meta        Meta              `json:"meta,omitempty"`
	Validations []Validation      `json:"validations,omitempty"`
	Amendments  []Amendment       `json:"amendments,omitempty"`
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cue/ -run TestValueObject_CarriesAmendments -v`
Expected: PASS

- [ ] **Step 5: Verify the whole package builds**

Run: `go build ./internal/cue/ && go test ./internal/cue/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/cue/types.go internal/cue/amendment_test.go
git commit -m "feat(cue): thread amendments onto declaration structs"
```

### Task 3: `ResourceAmendments` helper

**Files:**
- Modify: `internal/cue/registry.go`
- Test: `internal/cue/amendment_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/cue/amendment_test.go`:

```go
func TestResourceAmendments_TypeSwitches(t *testing.T) {
	r := Resource{
		ID:   "Audio.EqualTemperament",
		Kind: "valueObject",
		Declaration: ValueObject{
			Amendments: []Amendment{{Name: "a1"}, {Name: "a2"}},
		},
	}
	got := ResourceAmendments(r)
	if len(got) != 2 || got[1].Name != "a2" {
		t.Fatalf("expected 2 amendments, got %+v", got)
	}

	empty := Resource{Declaration: Aggregate{}}
	if len(ResourceAmendments(empty)) != 0 {
		t.Fatalf("expected no amendments for empty aggregate")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cue/ -run TestResourceAmendments_TypeSwitches -v`
Expected: FAIL — `undefined: ResourceAmendments`

- [ ] **Step 3: Implement the helper**

In `internal/cue/registry.go`, add:

```go
// ResourceAmendments extracts the amendments list from a resource's declaration,
// regardless of its concrete declaration type. Returns nil when the declaration
// kind does not carry amendments or has none.
func ResourceAmendments(r Resource) []Amendment {
	switch d := r.Declaration.(type) {
	case Aggregate:
		return d.Amendments
	case ValueObject:
		return d.Amendments
	case Entity:
		return d.Amendments
	case Asset:
		return d.Amendments
	case Adapter:
		return d.Amendments
	default:
		return nil
	}
}
```

(If grep in Task 2 revealed additional declaration structs carrying `Amendments`, add matching cases here.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/cue/ -run TestResourceAmendments_TypeSwitches -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cue/registry.go internal/cue/amendment_test.go
git commit -m "feat(cue): ResourceAmendments declaration-agnostic accessor"
```

### Task 4: Prove an amendment triggers `ActionModify`

**Files:**
- Test: `internal/plan/planner_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/plan/planner_test.go` a test that mirrors the existing planner tests' fixture style. (First read the top of `planner_test.go` to reuse its helpers for building a registry + store; match their construction exactly.) The assertion:

```go
func TestPlan_AmendmentChangesDeclarationHash(t *testing.T) {
	vo := cue.ValueObject{From: "f64", Invariants: []string{"finite"}}
	before := declHashForTest(vo) // see Step 3 helper

	voAmended := vo
	voAmended.Amendments = []cue.Amendment{{Name: "validate", Prompt: "reject NaN"}}
	after := declHashForTest(voAmended)

	if before == after {
		t.Fatalf("adding an amendment must change the declaration hash (before==after==%s)", before)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/plan/ -run TestPlan_AmendmentChangesDeclarationHash -v`
Expected: FAIL — `undefined: declHashForTest`

- [ ] **Step 3: Add a tiny test helper that calls the real `declHash`**

`declHash` is unexported in `internal/plan/planner.go`. Add this helper in `planner_test.go` (same package `plan`):

```go
func declHashForTest(decl any) string { return declHash(decl) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/plan/ -run TestPlan_AmendmentChangesDeclarationHash -v`
Expected: PASS — confirms the spec-hash → ActionModify path picks up amendments with zero planner changes.

- [ ] **Step 5: Commit**

```bash
git add internal/plan/planner_test.go
git commit -m "test(plan): amendment changes declaration hash (triggers ActionModify)"
```

---

## Phase 2 — SQLite materialized state + reconciliation

### Task 5: `amendments` table migration

**Files:**
- Create: `migrations/013_amendments.sql`

- [ ] **Step 1: Write the migration**

Create `migrations/013_amendments.sql` (mirrors the learnings DDL style in `migrations/012_learnings.sql`):

```sql
-- Resource-scoped spec amendments. Materialized cache over the CUE source of
-- truth: `state` and `applied_spec_hash` are recomputed from spec-vs-committed
-- hash during plan/begin reconciliation. Never an independent authority.
CREATE TABLE IF NOT EXISTS amendments (
    id                TEXT PRIMARY KEY,
    resource_id       TEXT NOT NULL,
    name              TEXT NOT NULL,
    content_hash      TEXT NOT NULL,
    origin            TEXT NOT NULL DEFAULT 'manual',
    prompt            TEXT NOT NULL DEFAULT '',
    finding_json      TEXT NOT NULL DEFAULT '',
    validation_json   TEXT NOT NULL DEFAULT '',
    state             TEXT NOT NULL DEFAULT 'PENDING'
                      CHECK (state IN ('PENDING','APPLIED','VERIFIED','GRADUATED','FAILED')),
    applied_spec_hash TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    applied_at        TEXT NOT NULL DEFAULT '',
    graduated_at      TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX idx_amendments_resource_name ON amendments(resource_id, name);
CREATE INDEX idx_amendments_state ON amendments(state);
```

- [ ] **Step 2: Verify it applies (migration runner runs on store open)**

Run: `go test ./internal/store/ -run TestStore -count=1`
Expected: PASS (store opens, migrations apply cleanly — no SQL error). If there is no broad store-open test, write a one-line `TestMigrations_Apply` that calls `store.New(t.TempDir()+"/x.db")` and asserts no error.

- [ ] **Step 3: Commit**

```bash
git add migrations/013_amendments.sql
git commit -m "feat(db): amendments table"
```

### Task 6: sqlc queries + regenerate

**Files:**
- Create: `sql/queries/amendments.sql`
- Generated (do not hand-edit): `internal/db/amendments.sql.go`, `internal/db/models.go`

- [ ] **Step 1: Write the query file**

Create `sql/queries/amendments.sql` (mirrors `sql/queries/learnings.sql`):

```sql
-- name: UpsertAmendment :exec
INSERT INTO amendments (id, resource_id, name, content_hash, origin, prompt, finding_json, validation_json, state, applied_spec_hash, created_at, applied_at, graduated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(resource_id, name) DO UPDATE SET
    content_hash      = excluded.content_hash,
    origin            = excluded.origin,
    prompt            = excluded.prompt,
    finding_json      = excluded.finding_json,
    validation_json   = excluded.validation_json,
    state             = excluded.state,
    applied_spec_hash = excluded.applied_spec_hash;

-- name: ListAmendmentsByResource :many
SELECT * FROM amendments WHERE resource_id = ? ORDER BY created_at ASC;

-- name: ListAmendmentsByState :many
SELECT * FROM amendments WHERE state = ? ORDER BY created_at ASC;

-- name: ListAllAmendments :many
SELECT * FROM amendments ORDER BY created_at ASC;

-- name: GetAmendment :one
SELECT * FROM amendments WHERE resource_id = ? AND name = ?;

-- name: UpdateAmendmentState :exec
UPDATE amendments SET state = ?, applied_spec_hash = ?, applied_at = ?, graduated_at = ? WHERE id = ?;

-- name: DeleteAmendment :exec
DELETE FROM amendments WHERE resource_id = ? AND name = ?;
```

- [ ] **Step 2: Regenerate sqlc**

Run: `make sqlc`
Expected: regenerates `internal/db/amendments.sql.go` and adds `Amendment` to `internal/db/models.go`. No errors.

- [ ] **Step 3: Verify build**

Run: `go build ./internal/db/`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add sql/queries/amendments.sql internal/db/amendments.sql.go internal/db/models.go
git commit -m "feat(db): amendments sqlc queries"
```

### Task 7: store domain type + CRUD wrappers

**Files:**
- Modify: `internal/store/store.go`
- Test: `internal/store/amendments_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/store/amendments_test.go`:

```go
package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStore_AmendmentRoundTrip(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	a := Amendment{
		ID:          "am1",
		ResourceID:  "Audio.EqualTemperament",
		Name:        "validate-reference-pitch",
		ContentHash: "abc123",
		Origin:      "deep_review",
		Prompt:      "reject NaN",
		State:       "PENDING",
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.UpsertAmendment(a); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := s.ListAmendmentsByResource("Audio.EqualTemperament")
	if err != nil || len(got) != 1 || got[0].Name != "validate-reference-pitch" {
		t.Fatalf("list mismatch: %v %+v", err, got)
	}
	// Upsert is idempotent on (resource_id, name).
	if err := s.UpsertAmendment(a); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = s.ListAmendmentsByResource("Audio.EqualTemperament")
	if len(got) != 1 {
		t.Fatalf("expected idempotent upsert, got %d rows", len(got))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestStore_AmendmentRoundTrip -v`
Expected: FAIL — `undefined: Amendment` / `UpsertAmendment`

- [ ] **Step 3: Add domain type, converter, and wrappers**

In `internal/store/store.go` (mirror the `Learning` block at the bottom of the file):

```go
// Amendment is the store's domain type for a resource-scoped spec amendment.
type Amendment struct {
	ID              string
	ResourceID      string
	Name            string
	ContentHash     string
	Origin          string
	Prompt          string
	FindingJSON     string
	ValidationJSON  string
	State           string
	AppliedSpecHash string
	CreatedAt       time.Time
	AppliedAt       time.Time
	GraduatedAt     time.Time
}

func dbAmendmentToAmendment(a db.Amendment) Amendment {
	return Amendment{
		ID:              a.ID,
		ResourceID:      a.ResourceID,
		Name:            a.Name,
		ContentHash:     a.ContentHash,
		Origin:          a.Origin,
		Prompt:          a.Prompt,
		FindingJSON:     a.FindingJson,
		ValidationJSON:  a.ValidationJson,
		State:           a.State,
		AppliedSpecHash: a.AppliedSpecHash,
		CreatedAt:       parseTime(a.CreatedAt),
		AppliedAt:       parseTime(a.AppliedAt),
		GraduatedAt:     parseTime(a.GraduatedAt),
	}
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// UpsertAmendment inserts or updates an amendment keyed by (resource_id, name).
func (s *Store) UpsertAmendment(a Amendment) error {
	if a.State == "" {
		a.State = "PENDING"
	}
	if a.Origin == "" {
		a.Origin = "manual"
	}
	return s.queries.UpsertAmendment(context.Background(), db.UpsertAmendmentParams{
		ID:              a.ID,
		ResourceID:      a.ResourceID,
		Name:            a.Name,
		ContentHash:     a.ContentHash,
		Origin:          a.Origin,
		Prompt:          a.Prompt,
		FindingJson:     a.FindingJSON,
		ValidationJson:  a.ValidationJSON,
		State:           a.State,
		AppliedSpecHash: a.AppliedSpecHash,
		CreatedAt:       fmtTime(a.CreatedAt),
		AppliedAt:       fmtTime(a.AppliedAt),
		GraduatedAt:     fmtTime(a.GraduatedAt),
	})
}

func (s *Store) ListAmendmentsByResource(resourceID string) ([]Amendment, error) {
	rows, err := s.queries.ListAmendmentsByResource(context.Background(), resourceID)
	if err != nil {
		return nil, err
	}
	out := make([]Amendment, len(rows))
	for i, r := range rows {
		out[i] = dbAmendmentToAmendment(r)
	}
	return out, nil
}

func (s *Store) ListAmendmentsByState(state string) ([]Amendment, error) {
	rows, err := s.queries.ListAmendmentsByState(context.Background(), state)
	if err != nil {
		return nil, err
	}
	out := make([]Amendment, len(rows))
	for i, r := range rows {
		out[i] = dbAmendmentToAmendment(r)
	}
	return out, nil
}

func (s *Store) ListAllAmendments() ([]Amendment, error) {
	rows, err := s.queries.ListAllAmendments(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]Amendment, len(rows))
	for i, r := range rows {
		out[i] = dbAmendmentToAmendment(r)
	}
	return out, nil
}

func (s *Store) GetAmendment(resourceID, name string) (*Amendment, error) {
	r, err := s.queries.GetAmendment(context.Background(), db.GetAmendmentParams{
		ResourceID: resourceID,
		Name:       name,
	})
	if err != nil {
		return nil, err
	}
	a := dbAmendmentToAmendment(r)
	return &a, nil
}

// UpdateAmendmentState rewrites derived state for one amendment.
func (s *Store) UpdateAmendmentState(id, state, appliedSpecHash string, appliedAt, graduatedAt time.Time) error {
	return s.queries.UpdateAmendmentState(context.Background(), db.UpdateAmendmentStateParams{
		State:           state,
		AppliedSpecHash: appliedSpecHash,
		AppliedAt:       fmtTime(appliedAt),
		GraduatedAt:     fmtTime(graduatedAt),
		ID:              id,
	})
}

func (s *Store) DeleteAmendment(resourceID, name string) error {
	return s.queries.DeleteAmendment(context.Background(), db.DeleteAmendmentParams{
		ResourceID: resourceID,
		Name:       name,
	})
}
```

> Note: confirm sqlc generated field names `FindingJson`/`ValidationJson` (sqlc title-cases `finding_json` → `FindingJson`). If it generated `FindingJSON`, adjust the converter/wrapper accordingly — the generated file is authoritative.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestStore_AmendmentRoundTrip -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/amendments_test.go
git commit -m "feat(store): amendments CRUD wrappers"
```

### Task 8: reconciliation — derive amendment state from spec vs. committed hash

**Files:**
- Create: `internal/spec/amendments.go`
- Test: `internal/spec/amendments_test.go` (create)

- [ ] **Step 1: Write the failing test**

Create `internal/spec/amendments_test.go`. (First read `internal/spec/amend_test.go` to reuse its `Spec` + mock-store construction harness verbatim — match field names and constructors.) Test the reconciler:

```go
// TestReconcileAmendments_PendingUntilCommitted: an amendment present in the
// spec whose resource has NOT been committed since (stored declaration hash
// excludes it) is PENDING; once the stored hash equals the current declaration
// hash, it becomes APPLIED.
func TestReconcileAmendments_PendingUntilCommitted(t *testing.T) {
	// build a Spec with a registry where ValueObject "Audio.EqualTemperament"
	// carries one amendment, and a store whose stored resource declaration hash
	// does NOT include the amendment yet.
	// ... (use the amend_test.go harness) ...
	// call s.ReconcileAmendments(ctx)
	// assert the row exists with state == "PENDING".

	// Then SetResource with DeclarationHash == current declHash(declaration incl. amendment)
	// call s.ReconcileAmendments(ctx) again
	// assert state == "APPLIED" and applied_spec_hash == that hash.
}
```

Fill in the harness body using the patterns from `amend_test.go` (registry builder, mock store). Keep the two assertions above as the contract.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/spec/ -run TestReconcileAmendments -v`
Expected: FAIL — `undefined: ReconcileAmendments`

- [ ] **Step 3: Implement reconciliation**

Create `internal/spec/amendments.go`:

```go
package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	cuepkg "<module>/internal/cue" // use the repo's actual module path/alias
	"<module>/internal/store"
)

// amendmentContentHash is the stable identity of an amendment: hash of the
// fields a human authored (name, prompt, finding). Same content ⇒ same id.
func amendmentContentHash(a cuepkg.Amendment) string {
	payload, _ := json.Marshal(struct {
		Name    string          `json:"name"`
		Prompt  string          `json:"prompt"`
		Finding *cuepkg.Finding `json:"finding"`
	}{a.Name, a.Prompt, a.Finding})
	return fmt.Sprintf("%x", sha256.Sum256(payload))
}

// ReconcileAmendments rewrites the materialized amendments table from the CUE
// source of truth. For each amendment found on a resource in the current spec,
// its state is DERIVED: APPLIED iff the resource's stored declaration hash equals
// the current declaration hash (i.e. the committed output was generated from a
// spec snapshot that included this amendment); otherwise PENDING. The table is a
// cache — this function is the only writer of derived state during plan/begin.
func (s *Spec) ReconcileAmendments(ctx context.Context) error {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return fmt.Errorf("plan for amendment reconcile: %w", err)
	}
	for id, r := range planResult.Registry.Resources {
		ams := cuepkg.ResourceAmendments(r)
		if len(ams) == 0 {
			continue
		}
		declData, _ := json.Marshal(r.Declaration)
		currentDeclHash := fmt.Sprintf("%x", sha256.Sum256(declData))

		stored, _ := s.store.GetResource(id)
		committedIncludesAmendments := stored != nil && stored.DeclarationHash == currentDeclHash

		for _, a := range ams {
			findingJSON := ""
			if a.Finding != nil {
				b, _ := json.Marshal(a.Finding)
				findingJSON = string(b)
			}
			validationJSON := ""
			if a.Validation != nil {
				b, _ := json.Marshal(a.Validation)
				validationJSON = string(b)
			}
			state := "PENDING"
			appliedHash := ""
			appliedAt := time.Time{}
			if committedIncludesAmendments {
				state = "APPLIED"
				appliedHash = currentDeclHash
				appliedAt = time.Now().UTC()
			}
			if a.Graduated {
				state = "GRADUATED"
			}
			// Preserve an existing VERIFIED state: deep_review verification is a
			// stronger signal than the derived APPLIED. Reconcile never downgrades
			// VERIFIED→APPLIED while the amendment is still present and committed.
			if prior, _ := s.store.GetAmendment(id, a.Name); prior != nil &&
				prior.State == "VERIFIED" && committedIncludesAmendments {
				state = "VERIFIED"
			}
			if err := s.store.UpsertAmendment(store.Amendment{
				ID:              id + "::" + a.Name,
				ResourceID:      id,
				Name:            a.Name,
				ContentHash:     amendmentContentHash(a),
				Origin:          a.Origin,
				Prompt:          a.Prompt,
				FindingJSON:     findingJSON,
				ValidationJSON:  validationJSON,
				State:           state,
				AppliedSpecHash: appliedHash,
				CreatedAt:       parseAmendmentCreatedAt(a.CreatedAt),
				AppliedAt:       appliedAt,
			}); err != nil {
				return fmt.Errorf("upsert amendment %s/%s: %w", id, a.Name, err)
			}
		}
	}
	return nil
}

func parseAmendmentCreatedAt(s string) time.Time {
	if s == "" {
		return time.Now().UTC()
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Now().UTC()
	}
	return t
}
```

Replace `<module>` with the real module path (run `head -1 go.mod`). Confirm `s.store.GetResource` returns `(*store.Resource, error)` (it does — used in `resolve.go`).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/spec/ -run TestReconcileAmendments -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/spec/amendments.go internal/spec/amendments_test.go
git commit -m "feat(spec): reconcile amendment state from spec vs committed hash"
```

### Task 9: run reconciliation during plan/begin

**Files:**
- Modify: `internal/spec/session.go` (the `Begin` method)
- Test: `internal/spec/amendments_test.go`

- [ ] **Step 1: Write the failing test**

Append a test asserting that after `s.Begin(ctx, ...)` on a session whose spec has an amendment, `s.store.ListAmendmentsByResource(id)` returns the amendment with a derived state. (Reuse the Begin harness from existing session tests — read `internal/spec/session_test.go` for the constructor.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/spec/ -run TestBegin_ReconcilesAmendments -v`
Expected: FAIL — no amendment row after Begin.

- [ ] **Step 3: Call reconcile inside Begin**

In `internal/spec/session.go`, inside `Begin`, after the plan result is available and before returning, add:

```go
	// Materialize amendment lifecycle state from the spec (best-effort: a
	// reconcile failure must not block a generation session).
	if err := s.ReconcileAmendments(ctx); err != nil {
		log.Printf("amendments: reconcile during begin failed (swallowed): %v", err)
	}
```

(Match the existing logging idiom in the file — if it uses a struct logger rather than `log`, use that.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/spec/ -run TestBegin_ReconcilesAmendments -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/spec/session.go internal/spec/amendments_test.go
git commit -m "feat(spec): reconcile amendments on session begin"
```

---

## Phase 3 — generic UPDATE mode in spec_context

### Task 10: UPDATE-mode prompt template

**Files:**
- Create: `internal/prompt/templates/update.md`

- [ ] **Step 1: Write the template**

Create `internal/prompt/templates/update.md`:

```markdown
## UPDATE MODE — Modify Existing Code

This resource has **already been generated**. The files below are the current
committed implementation. Do **not** rewrite them from scratch.

Make the **minimal change** required by the "CHANGES TO MAKE" block below.
Preserve everything else — structure, naming, formatting, unrelated logic.
Output the full content of every file you change (using the `// path:` annotation),
and only those files. Unchanged files do not need to be re-emitted.
```

- [ ] **Step 2: Verify it is embedded**

The embed directive in `internal/prompt/templates.go` is `//go:embed templates/*.md templates/learned/*.md` — `update.md` is covered. Confirm:

Run: `go build ./internal/prompt/`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/prompt/templates/update.md
git commit -m "feat(prompt): UPDATE-mode template"
```

### Task 11: render existing-files + changes sections in RuntimeContext

**Files:**
- Modify: `internal/prompt/context.go`
- Test: `internal/prompt/context_test.go` (append; create if absent)

- [ ] **Step 1: Write the failing test**

Append to `internal/prompt/context_test.go`:

```go
func TestInjectRuntimeContext_UpdateMode(t *testing.T) {
	out := InjectRuntimeContext("BASE PROMPT", RuntimeContext{
		ExistingFiles:   map[string]string{"src/audio/equal_temperament.rs": "pub struct EqualTemperament;"},
		ChangesRequired: "Reject 0.0/NaN/inf reference pitches in ::new.",
	})
	if !strings.Contains(out, "Reject 0.0/NaN/inf") {
		t.Fatalf("expected CHANGES TO MAKE content, got:\n%s", out)
	}
	if !strings.Contains(out, "equal_temperament.rs") || !strings.Contains(out, "pub struct EqualTemperament;") {
		t.Fatalf("expected existing file content, got:\n%s", out)
	}
}
```

(Add `"strings"` to the test imports if missing.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/prompt/ -run TestInjectRuntimeContext_UpdateMode -v`
Expected: FAIL — `ExistingFiles`/`ChangesRequired` undefined.

- [ ] **Step 3: Extend RuntimeContext + injection**

In `internal/prompt/context.go`, add to the `RuntimeContext` struct:

```go
	// ExistingFiles is the current committed implementation, keyed by path. When
	// non-empty, the resource is regenerated in UPDATE mode (minimal diff).
	ExistingFiles map[string]string
	// ChangesRequired is the flagged "CHANGES TO MAKE" instruction block (e.g.
	// the concatenated prompts of pending amendments).
	ChangesRequired string
```

In `InjectRuntimeContext`, near the top of the section-assembly (before "Previous Errors"), add — and reference the `update.md` template for the framing:

```go
	if len(ctx.ExistingFiles) > 0 {
		b.WriteString("\n")
		b.WriteString(renderTemplate("update.md", "", ""))
		b.WriteString("\n### Existing Generated Files\n\n")
		paths := make([]string, 0, len(ctx.ExistingFiles))
		for p := range ctx.ExistingFiles {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			b.WriteString("`" + p + "`:\n\n```\n")
			b.WriteString(ctx.ExistingFiles[p])
			b.WriteString("\n```\n\n")
		}
		if ctx.ChangesRequired != "" {
			b.WriteString("### CHANGES TO MAKE\n\n")
			b.WriteString(ctx.ChangesRequired)
			b.WriteString("\n")
		}
	}
```

Add `"sort"` to the imports if not already present. (`renderTemplate` is in the same package — `internal/prompt/templates.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/prompt/ -run TestInjectRuntimeContext_UpdateMode -v`
Expected: PASS

- [ ] **Step 5: Confirm golden/placeholder tests still pass**

Run: `go test ./internal/prompt/`
Expected: PASS (the existing "no placeholder leaks" test must still pass — `update.md` has no `{{...}}`).

- [ ] **Step 6: Commit**

```bash
git add internal/prompt/context.go internal/prompt/context_test.go
git commit -m "feat(prompt): UPDATE-mode existing-files + changes sections"
```

### Task 12: populate UPDATE-mode context when a resource has committed output + pending amendments

**Files:**
- Modify: `internal/spec/runtime.go`
- Test: `internal/spec/runtime_test.go` (append; create if absent)

- [ ] **Step 1: Write the failing test**

Append a test that builds a `Spec` whose store reports `GetGeneratedFiles(id)` returning one file (with content readable through the mock `fs`) and a PENDING amendment for that resource, then calls `buildRuntimeContext` and asserts `ExistingFiles` is populated and `ChangesRequired` contains the amendment prompt. (Reuse the runtime/fs mock from existing spec tests.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/spec/ -run TestBuildRuntimeContext_UpdateMode -v`
Expected: FAIL — `ExistingFiles` empty.

- [ ] **Step 3: Populate the fields in buildRuntimeContext**

In `internal/spec/runtime.go`, after the existing dependency-file gathering and before returning, add:

```go
	// UPDATE mode: if this resource already has committed output, feed the
	// existing files back and flag any PENDING amendments as the changes to make.
	committed, _ := s.store.GetGeneratedFiles(resource.ID)
	if len(committed) > 0 {
		existing := make(map[string]string, len(committed))
		for _, f := range committed {
			data, err := s.fs.ReadFile(f.Path)
			if err != nil {
				continue
			}
			existing[f.Path] = string(data)
		}
		if len(existing) > 0 {
			rc.ExistingFiles = existing
		}
		if changes := s.pendingAmendmentChanges(resource.ID); changes != "" {
			rc.ChangesRequired = changes
		}
	}
```

(`rc` is the `prompt.RuntimeContext` value being assembled — match the local variable name already used in the function.)

Then add the helper in `internal/spec/amendments.go`:

```go
// pendingAmendmentChanges renders the concatenated prompts of all PENDING (or
// FAILED, i.e. not-yet-resolved) amendments for a resource into a single
// "changes to make" block. Empty when there are none.
func (s *Spec) pendingAmendmentChanges(resourceID string) string {
	ams, err := s.store.ListAmendmentsByResource(resourceID)
	if err != nil || len(ams) == 0 {
		return ""
	}
	var b strings.Builder
	for _, a := range ams {
		if a.State != "PENDING" && a.State != "FAILED" {
			continue
		}
		fmt.Fprintf(&b, "- **%s**: %s\n", a.Name, a.Prompt)
	}
	return strings.TrimRight(b.String(), "\n")
}
```

Add `"strings"` to the `amendments.go` imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/spec/ -run TestBuildRuntimeContext_UpdateMode -v`
Expected: PASS

- [ ] **Step 5: Full spec package test**

Run: `go test ./internal/spec/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/spec/runtime.go internal/spec/amendments.go internal/spec/runtime_test.go
git commit -m "feat(spec): feed existing files + pending amendments into UPDATE-mode context"
```

---

## Phase 4 — MCP tools (propose / apply / list / graduate)

### Task 13: CUE override write-back + diff renderer

**Files:**
- Create: `internal/spec/cuewrite.go`
- Test: `internal/spec/cuewrite_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/spec/cuewrite_test.go`:

```go
package spec

import (
	"strings"
	"testing"
)

func TestRenderAmendmentOverride(t *testing.T) {
	out := renderAmendmentOverride("crestsynth", "EqualTemperament", "valueObject", "Audio", []amendmentEntry{
		{Name: "validate-reference-pitch", Prompt: "reject 0.0/NaN/inf", Origin: "deep_review"},
	})
	for _, want := range []string{
		"package crestsynth",
		"// Amendment",
		"EqualTemperament",
		"amendments:",
		"validate-reference-pitch",
		"reject 0.0/NaN/inf",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("override missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/spec/ -run TestRenderAmendmentOverride -v`
Expected: FAIL — `undefined: renderAmendmentOverride`

- [ ] **Step 3: Implement the renderer + writer**

Create `internal/spec/cuewrite.go`:

```go
package spec

import (
	"fmt"
	"strings"
)

type amendmentEntry struct {
	Name    string
	Prompt  string
	Origin  string
	Finding *findingEntry
}

type findingEntry struct {
	Severity string
	File     string
	Line     int
	Text     string
}

// cuePath returns the CUE selector for a resource given its kind/context/name,
// matching the existing spec layout (project: contexts: <Ctx>: <bucket>: <Name>).
func cuePath(kind, contextName, name string) string {
	bucket := map[string]string{
		"valueObject":   "valueObjects",
		"aggregate":     "aggregates",
		"entity":        "entities",
		"asset":         "assets",
		"adapter":       "adapters",
	}[kind]
	if bucket == "" {
		bucket = kind + "s"
	}
	if contextName != "" && kind != "asset" && kind != "adapter" {
		return fmt.Sprintf("project: contexts: %s: %s: %s", contextName, bucket, name)
	}
	return fmt.Sprintf("project: %s: %s", bucket, name)
}

// renderAmendmentOverride produces a CUE override file body that appends the
// given amendments to a resource. CUE list fields unify by concatenation only
// with care; we therefore emit the full amendments list the caller computed
// (existing + new) so the file is self-contained and human-reviewable.
func renderAmendmentOverride(pkg, name, kind, contextName string, entries []amendmentEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	b.WriteString("// Amendment override (human-approved). Generated by spec/apply_amendments.\n")
	b.WriteString("// Each entry is a targeted, spec-resident correction; adding it re-renders\n")
	b.WriteString("// this resource in UPDATE mode. Edit or remove entries to change behavior.\n\n")
	fmt.Fprintf(&b, "%s: amendments: [\n", cuePath(kind, contextName, name))
	for _, e := range entries {
		b.WriteString("\t{\n")
		fmt.Fprintf(&b, "\t\tname:   %q\n", e.Name)
		fmt.Fprintf(&b, "\t\tprompt: %q\n", e.Prompt)
		if e.Origin != "" {
			fmt.Fprintf(&b, "\t\torigin: %q\n", e.Origin)
		}
		if e.Finding != nil {
			b.WriteString("\t\tfinding: {\n")
			if e.Finding.Severity != "" {
				fmt.Fprintf(&b, "\t\t\tseverity: %q\n", e.Finding.Severity)
			}
			if e.Finding.File != "" {
				fmt.Fprintf(&b, "\t\t\tfile: %q\n", e.Finding.File)
			}
			if e.Finding.Line != 0 {
				fmt.Fprintf(&b, "\t\t\tline: %d\n", e.Finding.Line)
			}
			if e.Finding.Text != "" {
				fmt.Fprintf(&b, "\t\t\ttext: %q\n", e.Finding.Text)
			}
			b.WriteString("\t\t}\n")
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("]\n")
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/spec/ -run TestRenderAmendmentOverride -v`
Expected: PASS

- [ ] **Step 5: Add a test that the rendered override is valid CUE that the loader accepts**

Append a test that writes `base.cue` (minimal) + the rendered override into a temp dir and calls `cue.Load(dir)`, asserting the resource's `Amendments` is populated. This guards against malformed CUE. (Use the loader from `internal/cue`.) If wiring a full project is heavy, instead assert via `cuecontext` that the snippet compiles.

- [ ] **Step 6: Commit**

```bash
git add internal/spec/cuewrite.go internal/spec/cuewrite_test.go
git commit -m "feat(spec): CUE amendment override renderer"
```

### Task 14: `ProposeAmendments` — LLM drafts amendments from findings

**Files:**
- Create: `internal/prompt/templates/propose_amendments.md`
- Modify: `internal/spec/amendments.go`
- Test: `internal/spec/amendments_test.go`

- [ ] **Step 1: Write the proposer prompt template**

Create `internal/prompt/templates/propose_amendments.md`:

```markdown
You are drafting **spec amendments** from code-review findings. For each
actionable finding, produce one amendment as a JSON object with fields:
`name` (stable kebab-case id), `prompt` (a precise, self-contained instruction
describing the change to make), and `finding` (echo the source severity/file/line/text).

Output ONLY a JSON array of amendment objects, nothing else. Skip findings that
are not actionable as a targeted code change.

Findings:
{{findings}}
```

- [ ] **Step 2: Write the failing test**

Append to `internal/spec/amendments_test.go` a test that injects a fake engine returning a known JSON array and asserts `ProposeAmendments` parses it into `[]ProposedAmendment` with the right `Name`/`Prompt`. (Reuse the fake `specEngine` from `review_test.go`/`dispatch_test.go`.)

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/spec/ -run TestProposeAmendments -v`
Expected: FAIL — `undefined: ProposeAmendments`

- [ ] **Step 4: Implement ProposeAmendments**

Add to `internal/spec/amendments.go`:

```go
// ProposedAmendment is a draft amendment (not yet written to the spec).
type ProposedAmendment struct {
	ResourceID string         `json:"resource_id"`
	Name       string         `json:"name"`
	Prompt     string         `json:"prompt"`
	Origin     string         `json:"origin"`
	Finding    *cuepkg.Finding `json:"finding,omitempty"`
}

// ProposeAmendments runs deep_review over the target (or a single resource) and
// asks the LLM to draft amendments from the findings. It writes nothing — the
// result is a proposal for human review, fed to ApplyAmendments on approval.
func (s *Spec) ProposeAmendments(ctx context.Context, sessionID, resourceID string) ([]ProposedAmendment, error) {
	review, err := s.DeepReview(ctx, DeepReviewOpts{SessionID: sessionID, Target: resourceID})
	if err != nil {
		return nil, fmt.Errorf("deep review for proposal: %w", err)
	}
	var proposals []ProposedAmendment
	for _, out := range review.Findings {
		for _, f := range out.Findings {
			prompt := renderProposePrompt(f)
			res, err := s.engine.CodeReview(ctx, engine.CodeReviewOpts{Prompt: prompt})
			if err != nil {
				continue
			}
			drafted := parseProposedAmendments(res.Output, resourceID)
			proposals = append(proposals, drafted...)
		}
	}
	return proposals, nil
}
```

Add `renderProposePrompt(finding)` (uses the `propose_amendments.md` template via `prompt.RenderNamed` — if no such exported helper exists, add a thin exported `prompt.RenderProposeAmendments(findings string) string` in `internal/prompt/` that calls the unexported `renderTemplate`). Add `parseProposedAmendments(output, resourceID string) []ProposedAmendment` that JSON-decodes the model output (tolerant: strips code fences). Import `engine` and `cuepkg` as already aliased.

> Decision: draft per-finding with one LLM call each keeps the prompt small and the mapping finding→amendment explicit. Batching is a later optimization, not needed for correctness.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/spec/ -run TestProposeAmendments -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/prompt/templates/propose_amendments.md internal/prompt/*.go internal/spec/amendments.go internal/spec/amendments_test.go
git commit -m "feat(spec): ProposeAmendments drafts amendments from review findings"
```

### Task 15: `ApplyAmendments` — human-gated CUE write-back

**Files:**
- Modify: `internal/spec/amendments.go`
- Test: `internal/spec/amendments_test.go`

- [ ] **Step 1: Write the failing test**

Append a test: with `apply=false`, `ApplyAmendments` returns a non-empty `Diff`/`OverridePath` and writes NOTHING (mock fs records no write). With `apply=true`, it writes the override file (mock fs records the write at the expected path) and upserts the amendment rows as PENDING. Mirror `TestPromoteLearnings`-style assertions.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/spec/ -run TestApplyAmendments -v`
Expected: FAIL — `undefined: ApplyAmendments`

- [ ] **Step 3: Implement ApplyAmendments**

Add to `internal/spec/amendments.go` (mirrors `PromoteLearnings`' preview/apply shape):

```go
// AmendmentApplyResult is the outcome of a (preview or applied) write-back.
type AmendmentApplyResult struct {
	OverridePath string `json:"override_path"`
	Diff         string `json:"diff"`    // the CUE that would be / was written
	Applied      bool   `json:"applied"`
	Count        int    `json:"count"`
}

// ApplyAmendments writes approved amendments for a resource into a CUE override
// file. Human-gated: apply=false returns the rendered override for review and
// mutates nothing; apply=true writes the file and materializes PENDING rows.
func (s *Spec) ApplyAmendments(ctx context.Context, resourceID string, proposals []ProposedAmendment, apply bool) (*AmendmentApplyResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan for apply_amendments: %w", err)
	}
	r, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}

	// Merge: existing amendments on the resource + the approved proposals, keyed
	// by name (proposal wins on conflict).
	existing := cuepkg.ResourceAmendments(r)
	merged := map[string]amendmentEntry{}
	for _, a := range existing {
		merged[a.Name] = toEntry(a)
	}
	for _, p := range proposals {
		merged[p.Name] = amendmentEntry{Name: p.Name, Prompt: p.Prompt, Origin: p.Origin, Finding: toFindingEntry(p.Finding)}
	}
	entries := make([]amendmentEntry, 0, len(merged))
	for _, e := range merged {
		entries = append(entries, e)
	}
	sortEntriesByName(entries)

	pkg := s.cuePackageName(planResult) // read from an existing spec file's package clause
	overridePath := s.amendmentOverridePath(resourceID, r.Kind, r.ContextName)
	body := renderAmendmentOverride(pkg, resourceShortName(resourceID), r.Kind, r.ContextName, entries)

	result := &AmendmentApplyResult{OverridePath: overridePath, Diff: body, Count: len(proposals)}
	if !apply {
		return result, nil
	}
	if err := s.fs.WriteFile(overridePath, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("write override: %w", err)
	}
	// Materialize PENDING rows immediately (reconcile will confirm on next begin).
	for _, p := range proposals {
		findingJSON := ""
		if p.Finding != nil {
			b, _ := json.Marshal(p.Finding)
			findingJSON = string(b)
		}
		s.store.UpsertAmendment(store.Amendment{
			ID: resourceID + "::" + p.Name, ResourceID: resourceID, Name: p.Name,
			ContentHash: amendmentContentHash(cuepkg.Amendment{Name: p.Name, Prompt: p.Prompt, Finding: p.Finding}),
			Origin: p.Origin, Prompt: p.Prompt, FindingJSON: findingJSON,
			State: "PENDING", CreatedAt: time.Now().UTC(),
		})
	}
	result.Applied = true
	return result, nil
}
```

Add the small helpers referenced: `toEntry(cuepkg.Amendment) amendmentEntry`, `toFindingEntry(*cuepkg.Finding) *findingEntry`, `sortEntriesByName([]amendmentEntry)`, `resourceShortName(id string) string` (last `.`-segment), `s.cuePackageName(planResult)` (read the `package` clause from any `.cue` in the spec dir; default `"crestsynth"`), and `s.amendmentOverridePath(resourceID, kind, contextName string) string` (returns `<specDir>/override-<ShortName>.cue` or a phase-numbered path — match the override convention; the spec dir is `s.cfg.SpecDir`). Implement each in `cuewrite.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/spec/ -run TestApplyAmendments -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/spec/amendments.go internal/spec/cuewrite.go internal/spec/amendments_test.go
git commit -m "feat(spec): human-gated ApplyAmendments CUE write-back"
```

### Task 16: `ListAmendments` + `GraduateAmendment`

**Files:**
- Modify: `internal/spec/amendments.go`
- Test: `internal/spec/amendments_test.go`

- [ ] **Step 1: Write the failing tests**

Append:
- `TestListAmendments_FilterByState` — upsert two amendments (PENDING, APPLIED), assert `ListAmendments(ctx, "", "PENDING")` returns one; `ListAmendments(ctx, resourceID, "")` returns by resource.
- `TestGraduateAmendment_PreviewVsApply` — `apply=false` returns a diff folding the amendment's intent into the resource's `invariants` and removing the amendment, writes nothing; `apply=true` writes the canonical-fold override, deletes the amendment row, and sets remaining materialized state. (For the fold, the canonical change is appending the amendment's `prompt` as an invariant string and dropping it from `amendments`.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/spec/ -run 'TestListAmendments|TestGraduateAmendment' -v`
Expected: FAIL — undefined functions.

- [ ] **Step 3: Implement both**

Add to `internal/spec/amendments.go`:

```go
// ListAmendments returns materialized amendments, optionally filtered by
// resource and/or state (empty string = no filter on that dimension).
func (s *Spec) ListAmendments(ctx context.Context, resourceID, state string) ([]store.Amendment, error) {
	var rows []store.Amendment
	var err error
	switch {
	case resourceID != "":
		rows, err = s.store.ListAmendmentsByResource(resourceID)
	case state != "":
		return s.store.ListAmendmentsByState(state)
	default:
		rows, err = s.store.ListAllAmendments()
	}
	if err != nil {
		return nil, err
	}
	if state == "" {
		return rows, nil
	}
	out := rows[:0]
	for _, r := range rows {
		if r.State == state {
			out = append(out, r)
		}
	}
	return out, nil
}

// GraduationResult is the outcome of a (preview or applied) graduation.
type GraduationResult struct {
	OverridePath string `json:"override_path"`
	Diff         string `json:"diff"`
	Applied      bool   `json:"applied"`
}

// GraduateAmendment folds a VERIFIED amendment's intent into the resource's
// canonical invariants and removes the amendment. Human-gated: apply=false
// previews the CUE diff; apply=true writes it and deletes the amendment row.
// (The post-graduation force-regen check is driven by the orchestrator, not here.)
func (s *Spec) GraduateAmendment(ctx context.Context, resourceID, name string, apply bool) (*GraduationResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan for graduate: %w", err)
	}
	r, ok := planResult.Registry.Resources[resourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", resourceID)
	}
	target, err := s.store.GetAmendment(resourceID, name)
	if err != nil {
		return nil, fmt.Errorf("amendment not found: %s/%s", resourceID, name)
	}
	if target.State != "VERIFIED" {
		return nil, fmt.Errorf("amendment %s is %s, not VERIFIED; cannot graduate", name, target.State)
	}

	// Rebuild amendments list WITHOUT the graduated one; fold its prompt into invariants.
	var remaining []amendmentEntry
	for _, a := range cuepkg.ResourceAmendments(r) {
		if a.Name == name {
			continue
		}
		remaining = append(remaining, toEntry(a))
	}
	invariants := append(existingInvariants(r), "graduated: "+target.Prompt)
	pkg := s.cuePackageName(planResult)
	overridePath := s.amendmentOverridePath(resourceID, r.Kind, r.ContextName)
	body := renderGraduationOverride(pkg, resourceShortName(resourceID), r.Kind, r.ContextName, invariants, remaining)

	result := &GraduationResult{OverridePath: overridePath, Diff: body, Applied: false}
	if !apply {
		return result, nil
	}
	if err := s.fs.WriteFile(overridePath, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("write graduation override: %w", err)
	}
	if err := s.store.DeleteAmendment(resourceID, name); err != nil {
		return nil, fmt.Errorf("delete graduated amendment: %w", err)
	}
	result.Applied = true
	return result, nil
}
```

Add `existingInvariants(r cuepkg.Resource) []string` (type-switch like `ResourceAmendments`, returning the declaration's `Invariants`) and `renderGraduationOverride(...)` (like `renderAmendmentOverride` but emits both an `invariants:` list and the trimmed `amendments:` list) in `cuewrite.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/spec/ -run 'TestListAmendments|TestGraduateAmendment' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/spec/amendments.go internal/spec/cuewrite.go internal/spec/amendments_test.go
git commit -m "feat(spec): ListAmendments and human-gated GraduateAmendment"
```

### Task 17: extend `specHandler` interface + register MCP tools

**Files:**
- Modify: `internal/mcp/server.go`
- Modify: `internal/mcp/tools.go`
- Test: `internal/mcp/tools_test.go` (append — match the existing tool-count/registration test if one exists)

- [ ] **Step 1: Write the failing test**

If `internal/mcp` has a test asserting the registered tool set, append the four new names (`spec/propose_amendments`, `spec/apply_amendments`, `spec/list_amendments`, `spec/graduate_amendment`) to its expectation. Otherwise add `TestTools_AmendmentToolsRegistered` that builds the server with a stub `specHandler` and asserts those four tools are present in the tool list.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/ -run AmendmentTools -v`
Expected: FAIL — tools not registered / interface unsatisfied.

- [ ] **Step 3: Extend the interface**

In `internal/mcp/server.go`, add to `specHandler` (use the actual `spec` package alias used in that file):

```go
	ProposeAmendments(ctx context.Context, sessionID, resourceID string) ([]specmod.ProposedAmendment, error)
	ApplyAmendments(ctx context.Context, resourceID string, proposals []specmod.ProposedAmendment, apply bool) (*specmod.AmendmentApplyResult, error)
	ListAmendments(ctx context.Context, resourceID, state string) ([]storemod.Amendment, error)
	GraduateAmendment(ctx context.Context, resourceID, name string, apply bool) (*specmod.GraduationResult, error)
```

(Match the existing import aliases in `server.go` — likely `spec` and `store`.)

- [ ] **Step 4: Register the tools**

In `internal/mcp/tools.go`, add arg structs near the others:

```go
type specProposeAmendmentsArgs struct {
	SessionID  string `json:"session_id"`
	ResourceID string `json:"resource_id"`
}
type specApplyAmendmentsArgs struct {
	ResourceID string                       `json:"resource_id"`
	Proposals  []spec.ProposedAmendment     `json:"proposals"`
	Apply      bool                         `json:"apply"`
}
type specListAmendmentsArgs struct {
	ResourceID string `json:"resource_id"`
	State      string `json:"state"`
}
type specGraduateAmendmentArgs struct {
	ResourceID string `json:"resource_id"`
	Name       string `json:"name"`
	Apply      bool   `json:"apply"`
}
```

And register (mirror `spec/context` and `spec/promote_learnings`):

```go
s.addTool(toolDef{
	Name: "spec/propose_amendments",
	Description: "Draft spec amendments from deep_review findings for a resource (or whole session). Returns proposals only — writes nothing. Review, then pass approved proposals to spec/apply_amendments.",
	InputSchema: json.RawMessage(`{"type":"object","properties":{"session_id":{"type":"string"},"resource_id":{"type":"string"}},"required":["session_id"]}`),
}, specTool("propose_amendments", func(ctx context.Context, a specProposeAmendmentsArgs) (any, error) {
	return s.spec.ProposeAmendments(ctx, a.SessionID, a.ResourceID)
}))

s.addTool(toolDef{
	Name: "spec/apply_amendments",
	Description: "Human-gated write-back: writes approved amendments into the CUE spec as an override file. apply=false (default) returns the CUE diff for review and writes nothing; apply=true writes it. After approval, normal plan/begin re-renders the resource in UPDATE mode.",
	InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string"},"proposals":{"type":"array","items":{"type":"object"}},"apply":{"type":"boolean"}},"required":["resource_id","proposals"]}`),
}, specTool("apply_amendments", func(ctx context.Context, a specApplyAmendmentsArgs) (any, error) {
	return s.spec.ApplyAmendments(ctx, a.ResourceID, a.Proposals, a.Apply)
}))

s.addTool(toolDef{
	Name: "spec/list_amendments",
	Description: "List materialized amendments, optionally filtered by resource_id and/or state (PENDING|APPLIED|VERIFIED|GRADUATED|FAILED).",
	InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string"},"state":{"type":"string"}}}`),
}, specTool("list_amendments", func(ctx context.Context, a specListAmendmentsArgs) (any, error) {
	return s.spec.ListAmendments(ctx, a.ResourceID, a.State)
}))

s.addTool(toolDef{
	Name: "spec/graduate_amendment",
	Description: "Human-gated: fold a VERIFIED amendment's intent into the resource's canonical invariants and remove the amendment. apply=false returns the CUE diff; apply=true writes it. Run a force clean regen afterward to confirm the intent survives without the amendment.",
	InputSchema: json.RawMessage(`{"type":"object","properties":{"resource_id":{"type":"string"},"name":{"type":"string"},"apply":{"type":"boolean"}},"required":["resource_id","name"]}`),
}, specTool("graduate_amendment", func(ctx context.Context, a specGraduateAmendmentArgs) (any, error) {
	return s.spec.GraduateAmendment(ctx, a.ResourceID, a.Name, a.Apply)
}))
```

(Use whatever the file's import alias for the spec package is — the explore showed handlers call `s.spec.X`.)

- [ ] **Step 5: Run test + build**

Run: `go build ./... && go test ./internal/mcp/ -run AmendmentTools -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/server.go internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): register amendment tools (propose/apply/list/graduate)"
```

---

## Phase 5 — validation wiring + docs + full verification

### Task 18: include an amendment's `validation` in the resource's commit validations

**Files:**
- Modify: `internal/spec/session.go` (`runCommitValidations` or where the per-resource validation set is assembled)
- Test: `internal/spec/amendments_test.go`

- [ ] **Step 1: Write the failing test**

Append a test asserting that when a resource carries an amendment with a `Validation`, the set of validations run on commit for that resource includes the amendment's validation command. (Reuse the commit-validation harness; assert against the collected validation list, not a live run.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/spec/ -run TestCommit_IncludesAmendmentValidation -v`
Expected: FAIL.

- [ ] **Step 3: Merge amendment validations into the resource's set**

In `internal/spec/session.go`, where the resource's validations are gathered before running them, append amendment validations:

```go
	// Amendment-declared validations prove intent (applied != fixed).
	for _, a := range cuepkg.ResourceAmendments(resource) {
		if a.Validation != nil {
			validations = append(validations, *a.Validation)
		}
	}
```

(Match the local var name holding the `[]cue.Validation` set; import the cue package alias already used in the file.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/spec/ -run TestCommit_IncludesAmendmentValidation -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/spec/session.go internal/spec/amendments_test.go
git commit -m "feat(spec): run amendment-declared validations on commit"
```

### Task 19: documentation

**Files:**
- Modify: `SPEC.md`

- [ ] **Step 1: Document the workflow + tools**

In `SPEC.md`, add an "Amendments" section covering: the lifecycle (PENDING→APPLIED→VERIFIED→GRADUATED→FAILED), that state is derived from spec hash, the four tools and their human-gated `apply` flag, UPDATE mode, and that amendments ride the spec-hash → `ActionModify` path (not file-hash drift). Update any tool enumeration/count.

- [ ] **Step 2: Commit**

```bash
git add SPEC.md
git commit -m "docs(spec): document amendments workflow and tools"
```

### Task 20: full build + test sweep

- [ ] **Step 1: Build and test everything**

Run: `go build ./... && go test ./...`
Expected: PASS (all green).

- [ ] **Step 2: Grep for accidental drift coupling**

Run: `grep -rn "DriftActions\|ActionDrift\|spec/drift" internal/spec/amendments.go internal/spec/cuewrite.go`
Expected: no matches — amendments must not depend on file-hash drift machinery.

- [ ] **Step 3: Commit any final fixups**

```bash
git add -A
git commit -m "chore(amendments): build + test sweep green"
```

---

## Acceptance criteria — REAL end-to-end run (not simulated)

After the tasks above, validate against the design's acceptance criteria using the live `crest-synth` workspace. This is a manual orchestrated run (the user wants real LLM calls + real file generation, never a simulation):

1. `deep_review` the `crest-synth` workspace; `propose_amendments` for the real `EqualTemperament` validation finding.
2. `apply_amendments apply=false` → review the CUE diff; `apply=true` → confirm it's written as `override-EqualTemperament.cue`.
3. `plan`/`begin` → confirm **only** `EqualTemperament` shows as needing update (its declaration hash changed), nothing else.
4. Dispatch → inspect the sub-agent's prompt (`spec/context`): confirm it received the existing `equal_temperament.rs` + the flagged "CHANGES TO MAKE" block, and that the resulting diff is **minimal**.
5. Confirm `make build` + `cargo test` + clippy pass, and the amendment's assertion (`EqualTemperament::try_new(0.0)` ⇒ `Err`) passes.
6. Re-run `deep_review` → finding gone ⇒ mark amendment VERIFIED (`UpdateAmendmentState`).
7. `graduate_amendment apply=true` → confirm the invariant appears in canonical spec, the amendment row is deleted, and a `force` clean regen still passes.

---

## Self-review notes

- **Spec coverage:** §4.1 CUE schema → Tasks 1-3; §4.2 SQLite → Tasks 5-7; §5 lifecycle/state → Task 8 (reconcile) + Task 16 (graduate) + Task 6 state CHECK; §6 loop fit → Task 9 (begin), Tasks 11-12 (context UPDATE mode), Task 18 (validations); §7 tool surface → Tasks 14-17; §8 steps → all phases; §9 edge cases (multiple amendments compose) → Task 12 concatenates all pending; (validation-never-passes / FAILED) → state machine + Task 18; (idempotency) → Task 7 upsert + Task 8 derived state.
- **Hash inclusion (§8.3):** Task 4 proves it without planner changes — the explore confirmed `declHash`/`ComputeEffectiveHashes` already marshal `Declaration`.
- **No file-hash drift coupling:** Task 20 step 2 enforces it.
- **Deferred/known-thin spots:** the exact override-file path convention (phase-numbered vs flat) and `cuePackageName` are resolved against the live spec dir in Task 15 — read `s.cfg.SpecDir` contents to match the existing layout before finalizing those helpers.
