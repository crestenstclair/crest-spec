package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/agent"
	"github.com/crestenstclair/crest-spec/internal/config"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/engine"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// amendmentSpecCUE is a minimal spec whose value object carries a single
// amendment in its meta.amendments, exercising ReconcileAmendments end to end.
const amendmentSpecCUE = `project: {
	name: "amend-project"
	layers: ["domain"]
	meta: {
		language: "go"
		style:    "DDD"
	}
	contexts: Synth: {
		purpose: "Audio synthesis engine"
		valueObjects: Frequency: {
			state: hz: "float64"
			invariants: ["hz > 0"]
			meta: amendments: [{
				name:   "clamp-upper-bound"
				prompt: "Clamp hz to at most 20000."
				origin: "deep_review"
				finding: {
					severity: "warning"
					file:     "frequency.go"
					line:     12
					text:     "no upper bound on hz"
				}
			}]
		}
	}
	invariants: [{text: "frequency must be positive"}]
}
`

func setupAmendmentsSpec(t *testing.T) (*Spec, *store.Store) {
	t.Helper()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spec.cue"), []byte(amendmentSpecCUE), 0o644))

	cfg := &config.Config{
		SpecDir:       dir,
		GenerateModel: "test-model",
		MaxRetries:    3,
	}

	s := New(nil, st, OSFileSystem{}, cfg)
	return s, st
}

func TestReconcileAmendments_PendingThenApplied(t *testing.T) {
	s, st := setupAmendmentsSpec(t)
	ctx := context.Background()

	// Discover the resource id carrying the amendment and its current decl hash.
	planResult, err := s.Plan(ctx)
	require.NoError(t, err)

	var amendID, currentDeclHash string
	for id, r := range planResult.Registry.Resources {
		if r.Kind != "valueObject" {
			continue
		}
		declData, _ := json.Marshal(r.Declaration)
		amendID = id
		currentDeclHash = fmt.Sprintf("%x", sha256.Sum256(declData))
		break
	}
	require.NotEmpty(t, amendID, "should find the value object carrying the amendment")

	// Case A: PENDING — no stored resource, so the declaration is uncommitted.
	require.NoError(t, s.ReconcileAmendments(ctx))

	rows, err := st.ListAmendmentsByResource(amendID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "PENDING", rows[0].State)
	assert.Equal(t, "clamp-upper-bound", rows[0].Name)

	// Case B: APPLIED — stored resource's declaration hash matches the current
	// declaration hash, so the amendment is committed.
	require.NoError(t, st.SetResource(store.Resource{
		ID:              amendID,
		Kind:            "valueObject",
		DeclarationHash: currentDeclHash,
		EffectiveHash:   "whatever",
		Model:           "test-model",
		SettledAt:       time.Now().UTC(),
	}))

	require.NoError(t, s.ReconcileAmendments(ctx))

	rows, err = st.ListAmendmentsByResource(amendID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "APPLIED", rows[0].State)
	assert.Equal(t, currentDeclHash, rows[0].AppliedSpecHash)
}

// TestBegin_ReconcilesAmendments verifies that starting a session materializes
// the amendments table from the spec — i.e. Begin calls ReconcileAmendments.
func TestBegin_ReconcilesAmendments(t *testing.T) {
	s, st := setupAmendmentsSpec(t)
	ctx := context.Background()

	// Discover the resource id carrying the amendment.
	planResult, err := s.Plan(ctx)
	require.NoError(t, err)

	var amendID string
	for id, r := range planResult.Registry.Resources {
		if r.Kind != "valueObject" {
			continue
		}
		amendID = id
		break
	}
	require.NotEmpty(t, amendID, "should find the value object carrying the amendment")

	// No reconcile has run yet — the amendments table is empty.
	rows, err := st.ListAmendmentsByResource(amendID)
	require.NoError(t, err)
	require.Len(t, rows, 0)

	// Begin must reconcile amendments as part of starting the session.
	_, err = s.Begin(ctx, BeginOpts{})
	require.NoError(t, err)

	rows, err = st.ListAmendmentsByResource(amendID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "clamp-upper-bound", rows[0].Name)
}

// TestProposeAmendments verifies the drafting path: a deep review surfaces a
// finding, the proposer drafts one amendment per finding, and the parsed result
// carries the passed resourceID. Writes nothing.
func TestProposeAmendments(t *testing.T) {
	ctx := context.Background()

	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	const resourceID = "valueObject.Audio.EqualTemperament"
	require.NoError(t, st.SetResource(store.Resource{
		ID:              resourceID,
		Kind:            "valueObject",
		ContextName:     "Audio",
		DeclarationHash: "h",
		EffectiveHash:   "e",
		Model:           "test-model",
		SettledAt:       time.Now().UTC(),
	}))

	// A real generated file on disk so DeepReview has code to review.
	dir := t.TempDir()
	codePath := filepath.Join(dir, "equal_temperament.rs")
	require.NoError(t, os.WriteFile(codePath, []byte("pub fn pitch(hz: f64) -> f64 { hz }\n"), 0o644))
	require.NoError(t, st.SetGeneratedFile(store.GeneratedFile{
		Path:        codePath,
		ResourceID:  resourceID,
		ContentHash: "c",
		PromptHash:  "p",
		Model:       "test-model",
		CreatedAt:   time.Now().UTC(),
	}))

	const draftJSON = `[{"name":"validate-reference-pitch","prompt":"reject 0.0/NaN/inf","finding":{"severity":"major","file":"src/audio/equal_temperament.rs","line":17,"text":"no validation"}}]`
	const reviewJSON = `{"passed":false,"findings":[{"severity":"major","description":"no validation","file":"src/audio/equal_temperament.rs","line":17}],"summary":"missing input validation"}`

	eng := &mockEngine{
		codeReviewFn: func(_ context.Context, opts engine.CodeReviewOpts) (*agent.RunResult, error) {
			// The proposer prompt is the amendment-drafting template; the deep
			// review prompt is the SOLID/clean-code reviewer. Dispatch on which.
			if strings.Contains(opts.Prompt, "spec amendments") {
				return &agent.RunResult{Output: draftJSON}, nil
			}
			return &agent.RunResult{Output: reviewJSON}, nil
		},
	}

	cfg := &config.Config{SpecDir: dir, GenerateModel: "test-model", MaxRetries: 3}
	s := New(eng, st, OSFileSystem{}, cfg)

	result, err := s.ProposeAmendments(ctx, "sess-1", resourceID)
	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "validate-reference-pitch", result[0].Name)
	assert.NotEmpty(t, result[0].Prompt)
	assert.Equal(t, resourceID, result[0].ResourceID)
	assert.Equal(t, "deep_review", result[0].Origin)
}

// applyAmendmentsBaseCUE is a minimal spec declaring a value object
// EqualTemperament in context Audio, so Plan/registry resolves the resource
// "valueObject.Audio.EqualTemperament" for ApplyAmendments to target.
const applyAmendmentsBaseCUE = `package crestsynth

project: name: "rt"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: EqualTemperament: {from: "f64"}
`

// TestApplyAmendments_PreviewVsApply exercises the human-gated write-back:
// apply=false returns the override diff and mutates nothing (no file, no rows);
// apply=true writes the override file and materializes a PENDING row.
func TestApplyAmendments_PreviewVsApply(t *testing.T) {
	ctx := context.Background()

	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "base.cue"), []byte(applyAmendmentsBaseCUE), 0o644))

	cfg := &config.Config{SpecDir: dir, GenerateModel: "test-model", MaxRetries: 3}
	s := New(nil, st, OSFileSystem{}, cfg)

	const resourceID = "valueObject.Audio.EqualTemperament"
	expectedOverridePath := filepath.Join(dir, "override-EqualTemperament.cue")

	proposals := []ProposedAmendment{{
		ResourceID: resourceID,
		Name:       "validate-reference-pitch",
		Prompt:     "reject 0.0/NaN/inf",
		Origin:     "deep_review",
	}}

	// Preview: apply=false returns the diff and writes nothing.
	preview, err := s.ApplyAmendments(ctx, resourceID, proposals, false)
	require.NoError(t, err)
	assert.False(t, preview.Applied)
	assert.Equal(t, expectedOverridePath, preview.OverridePath)
	assert.Contains(t, preview.Diff, "meta: amendments:")
	assert.Contains(t, preview.Diff, "validate-reference-pitch")

	_, statErr := os.Stat(expectedOverridePath)
	assert.True(t, os.IsNotExist(statErr), "preview must not write the override file")

	rows, err := st.ListAmendmentsByResource(resourceID)
	require.NoError(t, err)
	assert.Len(t, rows, 0, "preview must not materialize amendment rows")

	// Apply: apply=true writes the file and upserts a PENDING row.
	applied, err := s.ApplyAmendments(ctx, resourceID, proposals, true)
	require.NoError(t, err)
	assert.True(t, applied.Applied)
	assert.Equal(t, expectedOverridePath, applied.OverridePath)

	onDisk, err := os.ReadFile(expectedOverridePath)
	require.NoError(t, err, "apply must write the override file")
	assert.Equal(t, applied.Diff, string(onDisk))
	assert.Contains(t, string(onDisk), "validate-reference-pitch")

	rows, err = st.ListAmendmentsByResource(resourceID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "validate-reference-pitch", rows[0].Name)
	assert.Equal(t, "PENDING", rows[0].State)
}

// TestListAmendments_FilterByState exercises the three filter dimensions of
// ListAmendments: by state only, by resource only, and unfiltered.
func TestListAmendments_FilterByState(t *testing.T) {
	ctx := context.Background()

	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "base.cue"), []byte(applyAmendmentsBaseCUE), 0o644))
	cfg := &config.Config{SpecDir: dir, GenerateModel: "test-model", MaxRetries: 3}
	s := New(nil, st, OSFileSystem{}, cfg)

	const resA = "valueObject.Audio.EqualTemperament"
	const resB = "valueObject.Audio.Other"

	require.NoError(t, st.UpsertAmendment(store.Amendment{
		ID:         resA + "::pending-one",
		ResourceID: resA,
		Name:       "pending-one",
		Prompt:     "pending change",
		State:      "PENDING",
		CreatedAt:  time.Now().UTC(),
	}))
	require.NoError(t, st.UpsertAmendment(store.Amendment{
		ID:         resB + "::applied-one",
		ResourceID: resB,
		Name:       "applied-one",
		Prompt:     "applied change",
		State:      "APPLIED",
		CreatedAt:  time.Now().UTC(),
	}))

	// Filter by state only.
	pending, err := s.ListAmendments(ctx, "", "PENDING")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "pending-one", pending[0].Name)

	// Filter by resource only.
	byResource, err := s.ListAmendments(ctx, resA, "")
	require.NoError(t, err)
	require.Len(t, byResource, 1)
	assert.Equal(t, "pending-one", byResource[0].Name)

	// No filter — all amendments.
	all, err := s.ListAmendments(ctx, "", "")
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

// TestGraduateAmendment_PreviewVsApply exercises the human-gated graduation:
// apply=false previews the CUE diff (folding the prompt into invariants) and
// mutates nothing; apply=true writes the override file, deletes the amendment
// row, and the resulting override is valid CUE (Plan reloads it). A non-VERIFIED
// amendment is rejected.
func TestGraduateAmendment_PreviewVsApply(t *testing.T) {
	ctx := context.Background()

	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "base.cue"), []byte(applyAmendmentsBaseCUE), 0o644))
	cfg := &config.Config{SpecDir: dir, GenerateModel: "test-model", MaxRetries: 3}
	s := New(nil, st, OSFileSystem{}, cfg)

	const resourceID = "valueObject.Audio.EqualTemperament"
	const name = "validate-reference-pitch"
	expectedOverridePath := filepath.Join(dir, "override-EqualTemperament.cue")

	// Author the resource so it carries the amendment in meta.amendments.
	proposals := []ProposedAmendment{{
		ResourceID: resourceID,
		Name:       name,
		Prompt:     "reject NaN",
		Origin:     "deep_review",
	}}
	_, err = s.ApplyAmendments(ctx, resourceID, proposals, true)
	require.NoError(t, err)

	// Put the amendment row in the store in VERIFIED state.
	require.NoError(t, st.UpsertAmendment(store.Amendment{
		ID:         resourceID + "::" + name,
		ResourceID: resourceID,
		Name:       name,
		Prompt:     "reject NaN",
		State:      "VERIFIED",
		CreatedAt:  time.Now().UTC(),
	}))

	// Remove the override file ApplyAmendments wrote, so we can assert preview
	// writes nothing and apply writes it back.
	require.NoError(t, os.Remove(expectedOverridePath))

	// Preview: apply=false returns the folded invariant diff, writes nothing,
	// does not delete the row.
	preview, err := s.GraduateAmendment(ctx, resourceID, name, false)
	require.NoError(t, err)
	assert.False(t, preview.Applied)
	assert.Equal(t, expectedOverridePath, preview.OverridePath)
	assert.Contains(t, preview.Diff, "invariants:")
	assert.Contains(t, preview.Diff, "reject NaN")

	_, statErr := os.Stat(expectedOverridePath)
	assert.True(t, os.IsNotExist(statErr), "preview must not write the override file")

	stillThere, err := st.GetAmendment(resourceID, name)
	require.NoError(t, err)
	require.NotNil(t, stillThere, "preview must not delete the amendment row")

	// Apply: writes the override, deletes the row, marks Applied.
	applied, err := s.GraduateAmendment(ctx, resourceID, name, true)
	require.NoError(t, err)
	assert.True(t, applied.Applied)
	assert.Equal(t, expectedOverridePath, applied.OverridePath)

	onDisk, err := os.ReadFile(expectedOverridePath)
	require.NoError(t, err, "apply must write the override file")
	assert.Contains(t, string(onDisk), "invariants:")

	gone, err := st.GetAmendment(resourceID, name)
	require.NoError(t, err)
	assert.Nil(t, gone, "apply must delete the graduated amendment row")

	// The graduation override must be valid CUE — Plan reloads it without error.
	_, err = s.Plan(ctx)
	require.NoError(t, err, "graduation override must be valid CUE")

	// Non-VERIFIED amendments cannot be graduated.
	require.NoError(t, st.UpsertAmendment(store.Amendment{
		ID:         resourceID + "::pending-grad",
		ResourceID: resourceID,
		Name:       "pending-grad",
		Prompt:     "not ready",
		State:      "PENDING",
		CreatedAt:  time.Now().UTC(),
	}))
	_, err = s.GraduateAmendment(ctx, resourceID, "pending-grad", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "VERIFIED")
}

// amendmentResourceWithValidation builds a value object resource carrying one
// amendment that declares a validation (so it participates in the commit
// verification gate) plus one amendment without a validation (untouched).
func amendmentResourceWithValidation(resourceID string) cuepkg.Resource {
	return cuepkg.Resource{
		ID:   resourceID,
		Kind: "valueObject",
		Declaration: cuepkg.ValueObject{
			Meta: cuepkg.Meta{
				Amendments: []cuepkg.Amendment{
					{
						Name:   "validated-change",
						Prompt: "do the validated thing",
						Validation: &cuepkg.Validation{
							Kind:    "test",
							Command: []string{"true"},
						},
					},
					{
						Name:   "no-validation",
						Prompt: "untouched here",
					},
				},
			},
		},
	}
}

// TestCommit_MarksAmendmentVerified drives the successful-commit verification
// write (via markAmendmentVerification, which Commit calls on the success path):
// an amendment that declares a validation flips APPLIED -> VERIFIED, and its
// existing applied_spec_hash / applied_at are PRESERVED (not wiped).
func TestCommit_MarksAmendmentVerified(t *testing.T) {
	s, st := setupAmendmentsSpec(t)
	const resourceID = "valueObject.Synth.Verified"

	appliedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, st.UpsertAmendment(store.Amendment{
		ID:              resourceID + "::validated-change",
		ResourceID:      resourceID,
		Name:            "validated-change",
		State:           "APPLIED",
		AppliedSpecHash: "decl-hash-abc",
		AppliedAt:       appliedAt,
		CreatedAt:       time.Now().UTC(),
	}))

	resource := amendmentResourceWithValidation(resourceID)
	s.markAmendmentVerification(resourceID, resource, true)

	got, err := st.GetAmendment(resourceID, "validated-change")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "VERIFIED", got.State)
	assert.Equal(t, "decl-hash-abc", got.AppliedSpecHash, "applied_spec_hash must be preserved")
	assert.Equal(t, appliedAt, got.AppliedAt, "applied_at must be preserved")

	// The amendment without a validation must be untouched (no row created).
	none, err := st.GetAmendment(resourceID, "no-validation")
	require.NoError(t, err)
	assert.Nil(t, none, "amendments without a validation must not be written")
}

// TestCommit_MarksAmendmentFailed drives the rejected-commit verification write
// (markAmendmentVerification(passed=false)): a validation-declaring amendment is
// marked FAILED, preserving its applied fields.
func TestCommit_MarksAmendmentFailed(t *testing.T) {
	s, st := setupAmendmentsSpec(t)
	const resourceID = "valueObject.Synth.Failed"

	require.NoError(t, st.UpsertAmendment(store.Amendment{
		ID:              resourceID + "::validated-change",
		ResourceID:      resourceID,
		Name:            "validated-change",
		State:           "APPLIED",
		AppliedSpecHash: "decl-hash-xyz",
		CreatedAt:       time.Now().UTC(),
	}))

	resource := amendmentResourceWithValidation(resourceID)
	s.markAmendmentVerification(resourceID, resource, false)

	got, err := st.GetAmendment(resourceID, "validated-change")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "FAILED", got.State)
	assert.Equal(t, "decl-hash-xyz", got.AppliedSpecHash, "applied_spec_hash must be preserved")
}

// TestReconcile_PreservesFailedWhenUncommitted verifies that a prior FAILED
// amendment stays FAILED across a reconcile while the resource is uncommitted
// (stored decl hash != current), rather than being reset to PENDING.
func TestReconcile_PreservesFailedWhenUncommitted(t *testing.T) {
	s, st := setupAmendmentsSpec(t)
	ctx := context.Background()

	planResult, err := s.Plan(ctx)
	require.NoError(t, err)

	var amendID string
	for id, r := range planResult.Registry.Resources {
		if r.Kind != "valueObject" {
			continue
		}
		amendID = id
		break
	}
	require.NotEmpty(t, amendID)

	// Seed the existing amendment row (the spec's "clamp-upper-bound") as FAILED.
	// No stored resource => uncommitted (stored decl hash != current).
	require.NoError(t, st.UpsertAmendment(store.Amendment{
		ID:         amendID + "::clamp-upper-bound",
		ResourceID: amendID,
		Name:       "clamp-upper-bound",
		State:      "FAILED",
		CreatedAt:  time.Now().UTC(),
	}))

	require.NoError(t, s.ReconcileAmendments(ctx))

	got, err := st.GetAmendment(amendID, "clamp-upper-bound")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "FAILED", got.State, "FAILED must be preserved while uncommitted, not reset to PENDING")
}
