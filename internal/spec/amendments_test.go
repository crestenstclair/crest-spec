package spec

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
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
