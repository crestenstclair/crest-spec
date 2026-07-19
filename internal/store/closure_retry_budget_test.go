package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
)

func TestAffectedClosureRepairCycleCountIsDurableAndExcludesTriageAndInfrastructure(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "state.db")
	st, err := New(databasePath)
	require.NoError(t, err)
	require.NoError(t, st.CreateApply("apply-1", "spec-hash"))
	require.NoError(t, st.CreateSession(Session{
		ID: "session-1", ApplyID: "apply-1", PlanJSON: "{}", WavesJSON: "[]", HashesJSON: "{}",
	}))
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: "resource.retry", State: "pending", MaxRetries: 3,
	}))

	createAttempt := func(id string) {
		t.Helper()
		_, createErr := st.CreateContextManifest(ctx, ContextManifestWrite{
			Manifest: testContextManifest(id, "manifest-"+id, "resource.retry"),
		})
		require.NoError(t, createErr)
	}
	classify := func(attemptID string, category execution.FailureCategory, origin, source string) *FailureClassification {
		t.Helper()
		failure, classifyErr := st.CreateFailureClassification(ctx, FailureClassificationWrite{
			AttemptID: attemptID, Category: category, Origin: origin, Confidence: 1,
			EvidenceSource: source, Evidence: "durable closure evidence",
		})
		require.NoError(t, classifyErr)
		return failure
	}
	assertCount := func(want int) {
		t.Helper()
		got, countErr := st.CountAffectedClosureRepairCycles(ctx, "session-1", "resource.retry")
		require.NoError(t, countErr)
		require.Equal(t, want, got)
	}

	createAttempt("attempt-triaged")
	triaged := classify(
		"attempt-triaged", execution.FailureIntegrationError, "engine", "affected_closure_validation",
	)
	assertCount(1)
	require.NoError(t, st.ResolveFailureClassification(
		ctx, triaged.ID, "triage attributed the diagnostic elsewhere", "",
	))
	assertCount(0)

	createAttempt("attempt-invalid-validation")
	classify(
		"attempt-invalid-validation", execution.FailureInvalidValidation, "engine", "affected_closure_validation",
	)
	createAttempt("attempt-host")
	classify(
		"attempt-host", execution.FailureIntegrationError, "host", "affected_closure_validation",
	)
	createAttempt("attempt-other-source")
	classify(
		"attempt-other-source", execution.FailureIntegrationError, "engine", "candidate_validation",
	)
	assertCount(0)

	createAttempt("attempt-first-cycle")
	firstCycle := classify(
		"attempt-first-cycle", execution.FailureIntegrationError, "engine", "affected_closure_validation",
	)
	assertCount(1)
	createAttempt("attempt-accepted-repair")
	require.NoError(t, st.ResolveFailureClassification(
		ctx, firstCycle.ID, "resolved by accepted retry", "attempt-accepted-repair",
	))
	assertCount(1)

	createAttempt("attempt-second-cycle")
	classify(
		"attempt-second-cycle", execution.FailureIntegrationError, "engine", "affected_closure_validation",
	)
	assertCount(2)
	maxCount, err := st.MaxAffectedClosureRepairCycles(ctx, "session-1")
	require.NoError(t, err)
	require.Equal(t, 2, maxCount)

	require.NoError(t, st.Close())
	restarted, err := New(databasePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Close()) })
	durableCount, err := restarted.CountAffectedClosureRepairCycles(ctx, "session-1", "resource.retry")
	require.NoError(t, err)
	require.Equal(t, 2, durableCount)
	durableMax, err := restarted.MaxAffectedClosureRepairCycles(ctx, "session-1")
	require.NoError(t, err)
	require.Equal(t, 2, durableMax)
}
