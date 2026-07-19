package store

import (
	"context"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/errors"
	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/stretchr/testify/require"
)

func TestAbandonSessionAtomicallyTerminatesUnfinishedWorkAndInvalidatesCommitted(t *testing.T) {
	st := newContextManifestStore(t, "resource.inflight")
	ctx := context.Background()
	require.NoError(t, st.AcquireLock("session-1", 101))

	require.NoError(t, st.SetResource(Resource{
		ID: "resource.committed", Kind: "aggregate", DeclarationHash: "decl-committed", EffectiveHash: "effective-committed",
	}))
	require.NoError(t, st.SetGeneratedFile(GeneratedFile{
		Path: "src/committed.go", ResourceID: "resource.committed", ContentHash: "content", PromptHash: "prompt", Model: "test",
	}))
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: "resource.committed", State: "committed", MaxRetries: 3,
	}))
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: "resource.pending", State: "pending", MaxRetries: 3,
	}))
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: "resource.skipped", State: "skipped", LastError: "previous reason", MaxRetries: 3,
	}))

	prepareAndClaimTestContext(t, st, testContextManifest("attempt-1", "manifest-1", "resource.inflight"))
	executionInput := testExecutionManifest()
	started, err := st.StartExecution(ctx, executionInput)
	require.NoError(t, err)
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: "src/inflight.go", Content: "package inflight\n", WriteIntent: "modify",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID, ToStatus: execution.StatusValidating,
	})
	require.NoError(t, err)

	result, err := st.AbandonSession(ctx, "session-1", "unsafe context manifests", true)
	require.NoError(t, err)
	require.Equal(t, "aborted", result.Status)
	require.Equal(t, 2, result.ResourcesSkipped)
	require.Equal(t, 1, result.AttemptsAbandoned)
	require.Equal(t, 1, result.ExecutionsCancelled)
	require.Equal(t, 1, result.CandidatesRejected)
	require.Equal(t, 1, result.CommittedInvalidated)
	require.True(t, result.ApplyCancelled)
	require.True(t, result.LockReleased)

	session, err := st.GetSession("session-1")
	require.NoError(t, err)
	require.Equal(t, "aborted", session.Status)
	apply, err := st.GetApply("apply-1")
	require.NoError(t, err)
	require.Equal(t, "cancelled", apply.Status)

	committed, err := st.GetSessionResource("session-1", "resource.committed")
	require.NoError(t, err)
	require.Equal(t, "committed", committed.State)
	pending, err := st.GetSessionResource("session-1", "resource.pending")
	require.NoError(t, err)
	require.Equal(t, "skipped", pending.State)
	require.Equal(t, "unsafe context manifests", pending.LastError)
	require.Equal(t, "abandoned", pending.Phase)
	inflight, err := st.GetSessionResource("session-1", "resource.inflight")
	require.NoError(t, err)
	require.Equal(t, "skipped", inflight.State)
	require.Empty(t, inflight.JobID)
	previouslySkipped, err := st.GetSessionResource("session-1", "resource.skipped")
	require.NoError(t, err)
	require.Equal(t, "previous reason", previouslySkipped.LastError)

	attempts, err := st.ListGenerationAttemptsBySession(ctx, "session-1")
	require.NoError(t, err)
	require.Len(t, attempts, 1)
	require.Equal(t, "abandoned", attempts[0].Status)
	cancelled, err := st.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusCancelled), cancelled.Status)
	require.Equal(t, "unsafe context manifests", cancelled.DispositionReason)
	require.Equal(t, "execution_cancelled", cancelled.Events[len(cancelled.Events)-1].Kind)
	require.JSONEq(t, `{"reason":"unsafe context manifests","session_id":"session-1","source":"spec/abandon"}`,
		cancelled.Events[len(cancelled.Events)-1].DetailsJSON)
	rejected, err := st.GetCandidateSetByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, candidate.ID, rejected.ID)
	require.Equal(t, "rejected", rejected.Status)
	require.Equal(t, "unsafe context manifests", rejected.DispositionReason)

	resource, err := st.GetResource("resource.committed")
	require.NoError(t, err)
	require.Equal(t, abandonedEffectiveHash("session-1", "resource.committed"), resource.EffectiveHash)
	require.Equal(t, "decl-committed", resource.DeclarationHash)
	owned, err := st.GetGeneratedFiles("resource.committed")
	require.NoError(t, err)
	require.Len(t, owned, 1, "abandonment must preserve accepted file ownership")
	_, err = st.GetLock()
	require.ErrorIs(t, err, errors.ErrNotFound)

	// Repeating an abandonment is safe and does not report the same work twice.
	repeated, err := st.AbandonSession(ctx, "session-1", "retry cleanup", true)
	require.NoError(t, err)
	require.Zero(t, repeated.ResourcesSkipped)
	require.Zero(t, repeated.AttemptsAbandoned)
	require.Zero(t, repeated.ExecutionsCancelled)
	require.Zero(t, repeated.CommittedInvalidated)
	require.False(t, repeated.ApplyCancelled)
	require.False(t, repeated.LockReleased)
}

func TestAbandonSessionPreservesAnotherSessionLockAndCommittedHashByDefault(t *testing.T) {
	st := newContextManifestStore(t, "resource.committed")
	ctx := context.Background()
	require.NoError(t, st.SetResource(Resource{
		ID: "resource.committed", Kind: "aggregate", DeclarationHash: "decl", EffectiveHash: "effective",
	}))
	require.NoError(t, st.UpdateSessionResourceState(
		"session-1", "resource.committed", "committed", "", "", 1, "attempt-accepted",
	))
	require.NoError(t, st.UpdateSessionResourcePhase(
		"session-1", "resource.committed", "verified", 1,
	))
	require.NoError(t, st.AcquireLock("session-new", 202))

	result, err := st.AbandonSession(ctx, "session-1", "superseded", false)
	require.NoError(t, err)
	require.False(t, result.LockReleased)
	require.Zero(t, result.CommittedInvalidated)

	lock, err := st.GetLock()
	require.NoError(t, err)
	require.Equal(t, "session-new", lock.Holder)
	resource, err := st.GetResource("resource.committed")
	require.NoError(t, err)
	require.Equal(t, "effective", resource.EffectiveHash)
}

func TestAbandonSessionInvalidatesProvisionalCommitByDefault(t *testing.T) {
	st := newContextManifestStore(t, "resource.provisional")
	ctx := context.Background()
	require.NoError(t, st.SetResource(Resource{
		ID: "resource.provisional", Kind: "aggregate", DeclarationHash: "decl", EffectiveHash: "provisional-effective",
	}))
	require.NoError(t, st.UpdateSessionResourceState(
		"session-1", "resource.provisional", "committed", "", "", 1, "attempt-accepted",
	))
	require.NoError(t, st.UpdateSessionResourcePhase(
		"session-1", "resource.provisional", "integration_pending", 1,
	))

	result, err := st.AbandonSession(ctx, "session-1", "interrupted before closure validation", false)
	require.NoError(t, err)
	require.Equal(t, 1, result.CommittedInvalidated)
	resource, err := st.GetResource("resource.provisional")
	require.NoError(t, err)
	require.Equal(t, abandonedEffectiveHash("session-1", "resource.provisional"), resource.EffectiveHash)
}

func TestAbandonSessionRejectsMissingReasonAndCompletedSession(t *testing.T) {
	st := newContextManifestStore(t, "resource.pending")
	ctx := context.Background()

	_, err := st.AbandonSession(ctx, "session-1", "  ", false)
	require.ErrorContains(t, err, "reason is required")
	require.NoError(t, st.UpdateSession("session-1", "completed", 0))
	_, err = st.AbandonSession(ctx, "session-1", "too late", false)
	require.ErrorContains(t, err, "terminal status completed")

	session, getErr := st.GetSession("session-1")
	require.NoError(t, getErr)
	require.Equal(t, "completed", session.Status)
	resource, getErr := st.GetSessionResource("session-1", "resource.pending")
	require.NoError(t, getErr)
	require.Equal(t, "pending", resource.State, "failed abandonment must roll back all changes")
}
