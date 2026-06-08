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
