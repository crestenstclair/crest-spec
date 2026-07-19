package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/stretchr/testify/require"
)

func TestFailDispatchReleasesExactClaimBeforeOrAfterAcknowledgementAndReplays(t *testing.T) {
	for _, acknowledged := range []bool{false, true} {
		t.Run(fmt.Sprintf("acknowledged=%t", acknowledged), func(t *testing.T) {
			const resourceID = "resource.dispatch.failed"
			st := newContextManifestStore(t, resourceID)
			ctx := context.Background()
			prepared := prepareStoredDispatchContext(t, st, resourceID, "attempt-1", "manifest-1")
			request := DispatchRequest{
				ID: prepared.ID, SessionID: "session-1", ResourceID: resourceID,
				HostSessionID: "host-session-1", WorkerID: "worker-1",
			}
			lifecycle, err := st.RequestDispatch(ctx, request)
			require.NoError(t, err)
			if acknowledged {
				lifecycle, err = st.AcknowledgeDispatch(ctx, request)
				require.NoError(t, err)
			}

			failed, err := st.FailDispatch(ctx, DispatchFailure{
				ID: request.ID, SessionID: request.SessionID, ResourceID: request.ResourceID,
				HostSessionID: request.HostSessionID, WorkerID: request.WorkerID,
				Reason: "Codex could not deliver the worker assignment",
			})
			require.NoError(t, err)
			require.Equal(t, lifecycle, failed, "failure recovery must not rewrite lifecycle observations")

			attempt, err := st.GetContextManifestByAttempt(ctx, "attempt-1")
			require.NoError(t, err)
			require.Equal(t, "abandoned", attempt.Attempt.Status)
			resource, err := st.GetSessionResource("session-1", resourceID)
			require.NoError(t, err)
			require.Equal(t, "pending", resource.State)
			require.Equal(t, "pre_execution_recovered", resource.Phase)
			require.Empty(t, resource.JobID)
			require.Zero(t, resource.Attempts)
			failures, err := st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
			require.NoError(t, err)
			require.Len(t, failures, 1)
			require.Equal(t, string(execution.FailureStaleContext), failures[0].Category)
			require.Equal(t, "engine", failures[0].Origin)
			require.Equal(t, DispatchFailureEvidenceSource, failures[0].EvidenceSource)
			require.Equal(t, prepared.ID, failures[0].EvidenceReference)
			require.Equal(t, "rebuild_context", failures[0].CorrectiveAction)
			require.False(t, failures[0].BlocksRetry)

			replayed, err := st.FailDispatch(ctx, DispatchFailure{
				ID: request.ID, SessionID: request.SessionID, ResourceID: request.ResourceID,
				HostSessionID: request.HostSessionID, WorkerID: request.WorkerID,
				Reason: "replayed delivery failure",
			})
			require.NoError(t, err)
			require.Equal(t, failed, replayed)
			failures, err = st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
			require.NoError(t, err)
			require.Len(t, failures, 1, "replay must not append another recovery audit")
		})
	}
}

func TestFailDispatchRequiresExactIdentityAndCannotCancelExecution(t *testing.T) {
	const resourceID = "resource.dispatch.exact"
	st := newContextManifestStore(t, resourceID)
	ctx := context.Background()
	prepared := prepareStoredDispatchContext(t, st, resourceID, "attempt-1", "manifest-1")
	request := DispatchRequest{
		ID: prepared.ID, SessionID: "session-1", ResourceID: resourceID,
		HostSessionID: "host-session-1", WorkerID: "worker-1",
	}
	_, err := st.RequestDispatch(ctx, request)
	require.NoError(t, err)
	_, err = st.FailDispatch(ctx, DispatchFailure{
		ID: request.ID, SessionID: request.SessionID, ResourceID: request.ResourceID,
		HostSessionID: request.HostSessionID, WorkerID: "other-worker", Reason: "delivery failed",
	})
	require.ErrorContains(t, err, "exact host worker assignment")
	_, err = st.FailDispatch(ctx, DispatchFailure{
		ID: request.ID, SessionID: request.SessionID, ResourceID: request.ResourceID,
		HostSessionID: request.HostSessionID, WorkerID: request.WorkerID,
	})
	require.ErrorContains(t, err, "reason is required")
	_, err = st.AcknowledgeDispatch(ctx, request)
	require.NoError(t, err)
	_, err = st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)
	_, err = st.FailDispatch(ctx, DispatchFailure{
		ID: request.ID, SessionID: request.SessionID, ResourceID: request.ResourceID,
		HostSessionID: request.HostSessionID, WorkerID: request.WorkerID, Reason: "too late",
	})
	require.ErrorContains(t, err, "execution already registered")
	failures, err := st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Empty(t, failures)
}

func TestFailDispatchRacesExecutionStartWithoutCancellingRegisteredWork(t *testing.T) {
	const iterations = 20
	for iteration := 0; iteration < iterations; iteration++ {
		t.Run("race", func(t *testing.T) {
			const resourceID = "resource.dispatch.start-race"
			st := newContextManifestStore(t, resourceID)
			st.sqlDB.SetMaxOpenConns(4)
			ctx := context.Background()
			prepared := prepareStoredDispatchContext(t, st, resourceID, "attempt-1", "manifest-1")
			request := DispatchRequest{
				ID: prepared.ID, SessionID: "session-1", ResourceID: resourceID,
				HostSessionID: "host-session-1", WorkerID: "worker-1",
			}
			_, err := st.RequestDispatch(ctx, request)
			require.NoError(t, err)
			_, err = st.AcknowledgeDispatch(ctx, request)
			require.NoError(t, err)

			startErrors := make(chan error, 1)
			failureErrors := make(chan error, 1)
			start := make(chan struct{})
			go func() {
				<-start
				input := testExecutionManifest()
				input.IdempotencyKey = fmt.Sprintf("dispatch-race-%d", iteration)
				_, startErr := st.StartExecution(ctx, input)
				startErrors <- startErr
			}()
			go func() {
				<-start
				_, failureErr := st.FailDispatch(ctx, DispatchFailure{
					ID: request.ID, SessionID: request.SessionID, ResourceID: request.ResourceID,
					HostSessionID: request.HostSessionID, WorkerID: request.WorkerID,
					Reason: "worker crashed before execution registration",
				})
				failureErrors <- failureErr
			}()
			close(start)
			startErr := <-startErrors
			failureErr := <-failureErrors
			require.NotEqual(t, startErr == nil, failureErr == nil,
				"exactly one terminal transition wins: start_err=%v dispatch_failed_err=%v", startErr, failureErr)

			attempt, err := st.GetContextManifestByAttempt(ctx, "attempt-1")
			require.NoError(t, err)
			resource, err := st.GetSessionResource("session-1", resourceID)
			require.NoError(t, err)
			failures, err := st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
			require.NoError(t, err)
			_, executionErr := st.GetExecutionManifestByAttempt(ctx, "attempt-1")
			if startErr == nil {
				require.NoError(t, executionErr)
				require.Equal(t, "context_prepared", attempt.Attempt.Status)
				require.Equal(t, "dispatched", resource.State)
				require.Empty(t, failures)
				require.ErrorContains(t, failureErr, "execution already registered")
				return
			}
			require.Error(t, executionErr)
			require.Equal(t, "abandoned", attempt.Attempt.Status)
			require.Equal(t, "pending", resource.State)
			require.Len(t, failures, 1)
			require.Error(t, startErr)
		})
	}
}

func prepareRecoveryContext(
	t *testing.T,
	st *Store,
	resourceID string,
	attemptID string,
	manifestID string,
	claimed bool,
) *DispatchLifecycle {
	t.Helper()
	ctx := context.Background()
	lifecycle, err := st.RequestContext(ctx, DispatchRequest{
		SessionID: "session-1", ResourceID: resourceID,
	})
	require.NoError(t, err)
	_, err = st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest:            testContextManifest(attemptID, manifestID, resourceID),
		DispatchLifecycleID: lifecycle.ID,
	})
	require.NoError(t, err)
	if claimed {
		lifecycle, err = st.RequestDispatch(ctx, DispatchRequest{
			ID: lifecycle.ID, SessionID: "session-1", ResourceID: resourceID,
			HostSessionID: "host-session-1", WorkerID: "worker-1",
		})
		require.NoError(t, err)
		lifecycle, err = st.AcknowledgeDispatch(ctx, DispatchRequest{
			ID: lifecycle.ID, SessionID: "session-1", ResourceID: resourceID,
			HostSessionID: "host-session-1", WorkerID: "worker-1",
		})
		require.NoError(t, err)
	} else {
		lifecycle, err = st.GetDispatchLifecycle(ctx, lifecycle.ID)
		require.NoError(t, err)
	}
	return lifecycle
}

func backdatePreparedRecoveryContext(t *testing.T, st *Store, attemptID, lifecycleID string) {
	t.Helper()
	const old = "2026-01-01T00:00:00Z"
	_, err := st.sqlDB.Exec(`UPDATE generation_attempts SET created_at = ? WHERE id = ?`, old, attemptID)
	require.NoError(t, err)
	_, err = st.sqlDB.Exec(`UPDATE dispatch_lifecycles SET updated_at = ? WHERE id = ?`, old, lifecycleID)
	require.NoError(t, err)
}

func TestRecoverStaleClaimedPreparedContextReleasesExactClaimAndAppendsAudit(t *testing.T) {
	const resourceID = "resource.recovery.claimed"
	st := newContextManifestStore(t, resourceID)
	ctx := context.Background()
	lifecycle := prepareRecoveryContext(t, st, resourceID, "attempt-1", "manifest-1", true)
	backdatePreparedRecoveryContext(t, st, "attempt-1", lifecycle.ID)
	lifecycleBefore, err := st.GetDispatchLifecycle(ctx, lifecycle.ID)
	require.NoError(t, err)

	recovered, err := st.RecoverStalePreExecutionAttempts(ctx, time.Now().UTC())
	require.NoError(t, err)
	require.Len(t, recovered, 1)
	require.True(t, recovered[0].Recovered)
	require.True(t, recovered[0].ClaimReleased)
	require.Equal(t, "attempt-1", recovered[0].AttemptID)

	attempt, err := st.GetContextManifestByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, "abandoned", attempt.Attempt.Status)
	resource, err := st.GetSessionResource("session-1", resourceID)
	require.NoError(t, err)
	require.Equal(t, "pending", resource.State)
	require.Equal(t, "pre_execution_recovered", resource.Phase)
	require.Empty(t, resource.JobID)
	require.Zero(t, resource.Attempts)
	require.Contains(t, resource.LastError, "no execution manifest or candidate")

	failures, err := st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Len(t, failures, 1)
	require.Equal(t, string(execution.FailureStaleContext), failures[0].Category)
	require.Equal(t, "engine", failures[0].Origin)
	require.Equal(t, PreExecutionRecoveryEvidenceSource, failures[0].EvidenceSource)
	require.Equal(t, "rebuild_context", failures[0].CorrectiveAction)
	require.Equal(t, lifecycle.ID, failures[0].EvidenceReference)

	lifecycleAfter, err := st.GetDispatchLifecycle(ctx, lifecycle.ID)
	require.NoError(t, err)
	require.Equal(t, lifecycleBefore, lifecycleAfter,
		"recovery audit is append-only and must not rewrite observed dispatch timestamps")

	repeated, err := st.RecoverStalePreExecutionAttempts(ctx, time.Now().UTC())
	require.NoError(t, err)
	require.Empty(t, repeated)
	failures, err = st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Len(t, failures, 1, "idempotent recovery must not duplicate its immutable audit record")
}

func TestAbandonPreparedContextLeavesUnclaimedResourceUntouchedAndNeverReleasesWrongOwner(t *testing.T) {
	const resourceID = "resource.recovery.unclaimed"
	st := newContextManifestStore(t, resourceID)
	ctx := context.Background()
	lifecycle := prepareRecoveryContext(t, st, resourceID, "attempt-1", "manifest-1", false)
	before, err := st.GetSessionResource("session-1", resourceID)
	require.NoError(t, err)

	result, err := st.AbandonPreparedContext(ctx, PreparedContextAbandonment{
		AttemptID: "attempt-1", Reason: "repository source changed", EvidenceSource: "source_provenance",
	})
	require.NoError(t, err)
	require.True(t, result.Recovered)
	require.False(t, result.ClaimReleased)
	after, err := st.GetSessionResource("session-1", resourceID)
	require.NoError(t, err)
	require.Equal(t, before, after, "unclaimed context recovery must not mutate the dispatchable resource")
	preserved, err := st.GetDispatchLifecycle(ctx, lifecycle.ID)
	require.NoError(t, err)
	require.Nil(t, preserved.DispatchRequestedAt)

	second := prepareRecoveryContext(t, st, resourceID, "attempt-2", "manifest-2", true)
	require.NoError(t, st.UpdateSessionResourceState(
		"session-1", resourceID, "dispatched", "new owner won", "", 0, "attempt-other",
	))
	lost, err := st.AbandonPreparedContext(ctx, PreparedContextAbandonment{
		AttemptID: "attempt-2", Reason: "stale snapshot",
	})
	require.NoError(t, err)
	require.True(t, lost.Recovered)
	require.False(t, lost.ClaimReleased)
	secondAttempt, err := st.GetContextManifestByAttempt(ctx, "attempt-2")
	require.NoError(t, err)
	require.Equal(t, "abandoned", secondAttempt.Attempt.Status)
	resource, err := st.GetSessionResource("session-1", resourceID)
	require.NoError(t, err)
	require.Equal(t, "attempt-other", resource.JobID)
	secondAfter, err := st.GetDispatchLifecycle(ctx, second.ID)
	require.NoError(t, err)
	require.Equal(t, second, secondAfter, "losing the resource CAS must leave lifecycle provenance untouched")
}

func TestRecoveredPreExecutionContextNeverInflatesRetryProjection(t *testing.T) {
	const resourceID = "resource.recovery.retry-budget"
	st := newContextManifestStore(t, resourceID)
	ctx := context.Background()
	prepareRecoveryContext(t, st, resourceID, "attempt-1", "manifest-1", false)
	recovered, err := st.AbandonPreparedContext(ctx, PreparedContextAbandonment{
		AttemptID: "attempt-1", Reason: "prepared context was never assigned",
	})
	require.NoError(t, err)
	require.True(t, recovered.Recovered)

	prepareRecoveryContext(t, st, resourceID, "attempt-2", "manifest-2", true)
	executionInput := testExecutionManifest()
	executionInput.AttemptID = "attempt-2"
	executionInput.ContextManifestID = "manifest-2"
	executionInput.IdempotencyKey = "retry-budget-execution"
	started, err := st.StartExecution(ctx, executionInput)
	require.NoError(t, err)
	count, err := st.CountExecutionAttempts(ctx, "session-1", resourceID)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: "retry_budget.go", Content: "package retrybudget\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID, ToStatus: execution.StatusValidating,
	})
	require.NoError(t, err)
	_, err = st.FinalizeCandidate(ctx, CandidateDisposition{
		ExecutionID: started.ID, Accepted: true, Reason: "first actual execution passed",
		Resource: &Resource{
			ID: resourceID, Kind: "asset", DeclarationHash: "declaration", EffectiveHash: "effective",
			GenerationInputHash: "generation", DependencyContractHash: "dependency", Model: "test-model",
		},
		Files: []GeneratedFile{{
			Path: "retry_budget.go", ResourceID: resourceID, ContentHash: candidate.Files[0].ContentHash,
			PromptHash: started.ContextHash, Model: "test-model",
		}},
		Attempts: 99, // Caller ordinals are not canonical retry accounting.
	})
	require.NoError(t, err)
	resource, err := st.GetSessionResource("session-1", resourceID)
	require.NoError(t, err)
	require.Equal(t, 1, resource.Attempts)
	generation, err := st.ListGenerations(resourceID, 10)
	require.NoError(t, err)
	require.Len(t, generation, 1)
	require.Zero(t, generation[0].RetryCount)
}

func TestAbandonPreparedContextRacesExecutionStartWithoutSplitBrain(t *testing.T) {
	const iterations = 20
	for iteration := 0; iteration < iterations; iteration++ {
		t.Run("race", func(t *testing.T) {
			const resourceID = "resource.recovery.race"
			st := newContextManifestStore(t, resourceID)
			st.sqlDB.SetMaxOpenConns(4)
			ctx := context.Background()
			lifecycle := prepareRecoveryContext(t, st, resourceID, "attempt-1", "manifest-1", true)

			type startResult struct {
				manifest *ExecutionManifest
				err      error
			}
			type abandonResult struct {
				recovery *PreparedContextRecovery
				err      error
			}
			startResults := make(chan startResult, 1)
			abandonResults := make(chan abandonResult, 1)
			start := make(chan struct{})
			go func() {
				<-start
				input := testExecutionManifest()
				input.IdempotencyKey = "race-start"
				manifest, err := st.StartExecution(ctx, input)
				startResults <- startResult{manifest: manifest, err: err}
			}()
			go func() {
				<-start
				recovery, err := st.AbandonPreparedContext(ctx, PreparedContextAbandonment{
					AttemptID: "attempt-1", Reason: "race recovery",
				})
				abandonResults <- abandonResult{recovery: recovery, err: err}
			}()
			close(start)
			started := <-startResults
			abandoned := <-abandonResults

			startWon := started.err == nil
			abandonWon := abandoned.err == nil && abandoned.recovery != nil && abandoned.recovery.Recovered
			require.NotEqual(t, startWon, abandonWon,
				"exactly one transition must win (start=%v abandon=%v start_err=%v abandon_err=%v)",
				startWon, abandonWon, started.err, abandoned.err)

			attempt, err := st.GetContextManifestByAttempt(ctx, "attempt-1")
			require.NoError(t, err)
			resource, err := st.GetSessionResource("session-1", resourceID)
			require.NoError(t, err)
			observedLifecycle, err := st.GetDispatchLifecycle(ctx, lifecycle.ID)
			require.NoError(t, err)
			_, executionErr := st.GetExecutionManifestByAttempt(ctx, "attempt-1")
			failures, err := st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
			require.NoError(t, err)

			if startWon {
				require.Equal(t, "context_prepared", attempt.Attempt.Status)
				require.Equal(t, "dispatched", resource.State)
				require.Equal(t, "attempt-1", resource.JobID)
				require.NotNil(t, observedLifecycle.ExecutionRegisteredAt)
				require.NoError(t, executionErr)
				require.Empty(t, failures)
				return
			}

			require.Equal(t, "abandoned", attempt.Attempt.Status)
			require.Equal(t, "pending", resource.State)
			require.Empty(t, resource.JobID)
			require.Nil(t, observedLifecycle.ExecutionRegisteredAt)
			require.Error(t, executionErr)
			require.Len(t, failures, 1)
			require.Equal(t, string(execution.FailureStaleContext), failures[0].Category)
			require.Error(t, started.err, "losing execution start must return an explicit error")
		})
	}
}
