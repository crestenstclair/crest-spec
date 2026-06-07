package spec

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func testdataDir(sub string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "cue", "testdata", sub)
}

func setupAmendSpec(t *testing.T) (*Spec, *store.Store) {
	t.Helper()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	cfg := &config.Config{
		SpecDir:       testdataDir("minimal"),
		GenerateModel: "test-model",
		MaxRetries:    3,
	}

	s := New(nil, st, OSFileSystem{}, cfg)
	return s, st
}

func TestAmend_RecomputesHashes(t *testing.T) {
	s, st := setupAmendSpec(t)
	ctx := context.Background()

	// Plan first to get the resource IDs and hashes
	planResult, err := s.Plan(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, planResult.Hashes)

	// Pick a non-structural resource
	var resourceID string
	for id, r := range planResult.Registry.Resources {
		if r.Kind != "project" && r.Kind != "context" && r.Kind != "assetKind" {
			resourceID = id
			break
		}
	}
	require.NotEmpty(t, resourceID, "should find at least one non-structural resource")

	// Seed the resource in the store with a stale hash
	err = st.SetResource(store.Resource{
		ID:              resourceID,
		Kind:            planResult.Registry.Resources[resourceID].Kind,
		DeclarationHash: "stale-decl-hash",
		EffectiveHash:   "stale-effective-hash",
		Model:           "test-model",
		SettledAt:       time.Now().UTC(),
	})
	require.NoError(t, err)

	// Create a session and apply for the amend
	applyID := "test-apply-1"
	sessionID := "test-session-1"
	require.NoError(t, st.CreateApply(applyID, "spec-hash"))
	require.NoError(t, st.CreateSession(store.Session{
		ID:         sessionID,
		ApplyID:    applyID,
		PlanJSON:   "[]",
		WavesJSON:  "[]",
		HashesJSON: "{}",
	}))

	// Run amend
	err = s.Amend(ctx, sessionID, resourceID)
	require.NoError(t, err)

	// Verify the resource's hashes were updated
	updated, err := st.GetResource(resourceID)
	require.NoError(t, err)
	assert.NotEqual(t, "stale-decl-hash", updated.DeclarationHash, "declaration hash should be recomputed")
	assert.NotEqual(t, "stale-effective-hash", updated.EffectiveHash, "effective hash should be recomputed")
	assert.Equal(t, planResult.Hashes[resourceID], updated.EffectiveHash, "effective hash should match plan")
}

func TestAmend_CascadesToDependents(t *testing.T) {
	s, st := setupAmendSpec(t)
	ctx := context.Background()

	// Plan to discover the dependency graph
	planResult, err := s.Plan(ctx)
	require.NoError(t, err)

	// Find a resource that has dependents (something other resources depend on)
	var sourceID string
	var dependentIDs []string
	for id := range planResult.Registry.Resources {
		deps := planResult.Graph.Dependents(id)
		if len(deps) > 0 {
			sourceID = id
			dependentIDs = deps
			break
		}
	}

	if sourceID == "" {
		t.Skip("no resource with dependents found in minimal testdata")
	}

	// Seed the source and its dependents in the store with known hashes
	for _, id := range append([]string{sourceID}, dependentIDs...) {
		r := planResult.Registry.Resources[id]
		err = st.SetResource(store.Resource{
			ID:              id,
			Kind:            r.Kind,
			DeclarationHash: "known-decl",
			EffectiveHash:   "known-effective",
			Model:           "test-model",
			SettledAt:       time.Now().UTC(),
		})
		require.NoError(t, err)
	}

	// Create session/apply
	applyID := "test-apply-cascade"
	sessionID := "test-session-cascade"
	require.NoError(t, st.CreateApply(applyID, "spec-hash"))
	require.NoError(t, st.CreateSession(store.Session{
		ID:         sessionID,
		ApplyID:    applyID,
		PlanJSON:   "[]",
		WavesJSON:  "[]",
		HashesJSON: "{}",
	}))

	// Run amend on the source
	err = s.Amend(ctx, sessionID, sourceID)
	require.NoError(t, err)

	// Verify dependents had their effective hashes invalidated (set to empty)
	for _, depID := range dependentIDs {
		dep, err := st.GetResource(depID)
		require.NoError(t, err)
		assert.Empty(t, dep.EffectiveHash, "dependent %s should have empty effective hash after cascade", depID)
	}
}

func TestAmend_RecordsAuditTrail(t *testing.T) {
	s, st := setupAmendSpec(t)
	ctx := context.Background()

	planResult, err := s.Plan(ctx)
	require.NoError(t, err)

	// Pick a non-structural resource
	var resourceID string
	for id, r := range planResult.Registry.Resources {
		if r.Kind != "project" && r.Kind != "context" && r.Kind != "assetKind" {
			resourceID = id
			break
		}
	}
	require.NotEmpty(t, resourceID)

	// Seed the resource
	err = st.SetResource(store.Resource{
		ID:              resourceID,
		Kind:            planResult.Registry.Resources[resourceID].Kind,
		DeclarationHash: "old",
		EffectiveHash:   "old",
		Model:           "test-model",
		SettledAt:       time.Now().UTC(),
	})
	require.NoError(t, err)

	// Create session/apply
	applyID := "test-apply-audit"
	sessionID := "test-session-audit"
	require.NoError(t, st.CreateApply(applyID, "spec-hash"))
	require.NoError(t, st.CreateSession(store.Session{
		ID:         sessionID,
		ApplyID:    applyID,
		PlanJSON:   "[]",
		WavesJSON:  "[]",
		HashesJSON: "{}",
	}))

	// Run amend
	err = s.Amend(ctx, sessionID, resourceID)
	require.NoError(t, err)

	// Verify audit trail was recorded
	actions, err := st.ListApplyActions(applyID)
	require.NoError(t, err)
	require.NotEmpty(t, actions, "should have at least one apply action")

	// Find the amend action
	var found bool
	for _, a := range actions {
		if a.Action == "amend" && a.ResourceID == resourceID {
			found = true
			assert.Equal(t, "success", a.Outcome)
			assert.Contains(t, a.Error, "dependents invalidated")
			break
		}
	}
	assert.True(t, found, "should find an amend action in the audit trail")
}

func TestAmend_ResetsSessionResourceToPending(t *testing.T) {
	s, st := setupAmendSpec(t)
	ctx := context.Background()

	planResult, err := s.Plan(ctx)
	require.NoError(t, err)

	// Pick a non-structural resource
	var resourceID string
	for id, r := range planResult.Registry.Resources {
		if r.Kind != "project" && r.Kind != "context" && r.Kind != "assetKind" {
			resourceID = id
			break
		}
	}
	require.NotEmpty(t, resourceID)

	// Seed the resource
	err = st.SetResource(store.Resource{
		ID:              resourceID,
		Kind:            planResult.Registry.Resources[resourceID].Kind,
		DeclarationHash: "old",
		EffectiveHash:   "old",
		Model:           "test-model",
		SettledAt:       time.Now().UTC(),
	})
	require.NoError(t, err)

	// Create session/apply
	applyID := "test-apply-reset"
	sessionID := "test-session-reset"
	require.NoError(t, st.CreateApply(applyID, "spec-hash"))
	require.NoError(t, st.CreateSession(store.Session{
		ID:         sessionID,
		ApplyID:    applyID,
		PlanJSON:   "[]",
		WavesJSON:  "[]",
		HashesJSON: "{}",
	}))

	// Put the resource in dispatched state in the session
	require.NoError(t, st.UpsertSessionResource(store.SessionResource{
		SessionID:  sessionID,
		ResourceID: resourceID,
		State:      string(StateDispatched),
		Attempts:   1,
		MaxRetries: 3,
	}))

	// Run amend
	err = s.Amend(ctx, sessionID, resourceID)
	require.NoError(t, err)

	// Verify the session resource was reset to pending
	sr, err := st.GetSessionResource(sessionID, resourceID)
	require.NoError(t, err)
	assert.Equal(t, string(StatePending), sr.State, "session resource should be reset to pending")
}

func TestAmend_ResourceNotFound(t *testing.T) {
	s, _ := setupAmendSpec(t)
	ctx := context.Background()

	st := s.store.(*store.Store)

	// Create session/apply
	applyID := "test-apply-notfound"
	sessionID := "test-session-notfound"
	require.NoError(t, st.CreateApply(applyID, "spec-hash"))
	require.NoError(t, st.CreateSession(store.Session{
		ID:         sessionID,
		ApplyID:    applyID,
		PlanJSON:   "[]",
		WavesJSON:  "[]",
		HashesJSON: "{}",
	}))

	// Amend a non-existent resource
	err := s.Amend(ctx, sessionID, "nonexistent.resource")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resource not found in registry")
}
