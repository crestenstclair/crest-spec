package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/stretchr/testify/require"
)

func TestClosureFailureRoutingRollsBackEveryCrashBoundaryBeforeEligibility(t *testing.T) {
	for _, crashStage := range []string{"after_failure", "after_handoff", "after_reopen"} {
		t.Run(crashStage, func(t *testing.T) {
			st := newAcceptedClosureRoutingStore(t, ":memory:")
			result, err := st.routeClosureValidationFailures(context.Background(), ClosureValidationFailureRoutingInput{
				SessionID: "session-1", ResourceIDs: []string{"resource.closure-routing"},
				Reason: "closure compiler failure", Category: execution.FailureIntegrationError,
				transitionHook: func(stage, resourceID string) error {
					if stage == crashStage {
						return errors.New("simulated process interruption")
					}
					return nil
				},
			})
			require.ErrorContains(t, err, "simulated process interruption")
			require.Nil(t, result)

			resource, err := st.GetSessionResource("session-1", "resource.closure-routing")
			require.NoError(t, err)
			require.Equal(t, "committed", resource.State,
				"a rollback must leave the owner ineligible for context dispatch")
			failures, err := st.ListFailureClassificationsByAttempt(context.Background(), "attempt-1")
			require.NoError(t, err)
			require.Empty(t, failures)
			handoffs, err := st.ListAttemptHandoffsBySource(context.Background(), "attempt-1")
			require.NoError(t, err)
			require.Empty(t, handoffs)
		})
	}
}

func TestClosureFailureRoutingRollsBackExhaustionCrashBoundary(t *testing.T) {
	ctx := context.Background()
	st := newAcceptedClosureRoutingStore(t, ":memory:")
	priorFailure, err := st.CreateFailureClassification(ctx, FailureClassificationWrite{
		AttemptID: "attempt-1", Category: execution.FailureIntegrationError,
		Origin: "engine", Confidence: 1, EvidenceSource: "affected_closure_validation",
		EvidenceReference: "session-1", Evidence: "prior closure compiler failure",
	})
	require.NoError(t, err)

	result, err := st.routeClosureValidationFailures(ctx, ClosureValidationFailureRoutingInput{
		SessionID: "session-1", ResourceIDs: []string{"resource.closure-routing"},
		Reason: "closure compiler failure", Category: execution.FailureIntegrationError,
		MaxRepairRetries: 1,
		transitionHook: func(stage, resourceID string) error {
			if stage == "after_exhausted" {
				return errors.New("simulated process interruption")
			}
			return nil
		},
	})
	require.ErrorContains(t, err, "simulated process interruption")
	require.Nil(t, result)

	resource, err := st.GetSessionResource("session-1", "resource.closure-routing")
	require.NoError(t, err)
	require.Equal(t, "committed", resource.State,
		"a rolled-back exhaustion projection must not expose a partial terminal state")
	failures, err := st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Len(t, failures, 1, "the in-transaction exhaustion classification must roll back")
	require.Equal(t, priorFailure.ID, failures[0].ID)
	handoffs, err := st.ListAttemptHandoffsBySource(ctx, "attempt-1")
	require.NoError(t, err)
	require.Empty(t, handoffs)
}

func TestClosureFailureRoutingPublishesPendingOwnerAndRouteInOneCommit(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "state.db")
	writer := newAcceptedClosureRoutingStore(t, databasePath)
	reader, err := New(databasePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })

	reachedUncommittedReopen := make(chan struct{})
	releaseCommit := make(chan struct{})
	resultChannel := make(chan *ClosureValidationFailureRoutingResult, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, routeErr := writer.routeClosureValidationFailures(context.Background(), ClosureValidationFailureRoutingInput{
			SessionID: "session-1", ResourceIDs: []string{"resource.closure-routing"},
			Reason: "closure compiler failure", Category: execution.FailureIntegrationError,
			transitionHook: func(stage, resourceID string) error {
				if stage == "after_reopen" {
					close(reachedUncommittedReopen)
					<-releaseCommit
				}
				return nil
			},
		})
		resultChannel <- result
		errorChannel <- routeErr
	}()
	<-reachedUncommittedReopen

	beforeCommit, err := reader.GetSessionResource("session-1", "resource.closure-routing")
	require.NoError(t, err)
	require.Equal(t, "committed", beforeCommit.State,
		"concurrent context scheduling may only observe the pre-route committed projection")
	beforeFailures, err := reader.ListFailureClassificationsByAttempt(context.Background(), "attempt-1")
	require.NoError(t, err)
	require.Empty(t, beforeFailures)
	beforeHandoffs, err := reader.ListAttemptHandoffsBySource(context.Background(), "attempt-1")
	require.NoError(t, err)
	require.Empty(t, beforeHandoffs)

	close(releaseCommit)
	result := <-resultChannel
	require.NoError(t, <-errorChannel)
	require.Equal(t, []string{"resource.closure-routing"}, result.ReopenedResources)

	afterCommit, err := reader.GetSessionResource("session-1", "resource.closure-routing")
	require.NoError(t, err)
	require.Equal(t, "pending", afterCommit.State)
	require.Equal(t, "integration_failed", afterCommit.Phase)
	afterFailures, err := reader.ListFailureClassificationsByAttempt(context.Background(), "attempt-1")
	require.NoError(t, err)
	require.Len(t, afterFailures, 1)
	afterHandoffs, err := reader.ListAttemptHandoffsBySource(context.Background(), "attempt-1")
	require.NoError(t, err)
	require.Len(t, afterHandoffs, 1)
	require.Equal(t, afterFailures[0].ID, afterHandoffs[0].FailureID)
	require.Equal(t, string(execution.RoleIntegrationImplementer), afterHandoffs[0].TargetRole)
}

func TestClosureFailureRoutingFailsClosedWithoutAcceptedAttemptProvenance(t *testing.T) {
	st := newContextManifestStore(t, "resource.no-provenance")
	require.NoError(t, st.UpdateSessionResourceState(
		"session-1", "resource.no-provenance", "committed", "", "", 0, "",
	))

	result, err := st.RouteClosureValidationFailures(context.Background(), ClosureValidationFailureRoutingInput{
		SessionID: "session-1", ResourceIDs: []string{"resource.no-provenance"},
		Reason: "closure compiler failure", Category: execution.FailureIntegrationError,
	})
	require.ErrorContains(t, err, "accepted attempt provenance")
	require.Nil(t, result)
	resource, err := st.GetSessionResource("session-1", "resource.no-provenance")
	require.NoError(t, err)
	require.Equal(t, "committed", resource.State)
}

func newAcceptedClosureRoutingStore(t *testing.T, databasePath string) *Store {
	t.Helper()
	st, err := New(databasePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	require.NoError(t, st.CreateApply("apply-1", "spec-hash"))
	require.NoError(t, st.CreateSession(Session{
		ID: "session-1", ApplyID: "apply-1", PlanJSON: "[]", WavesJSON: "[]", HashesJSON: "{}",
	}))
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: "resource.closure-routing",
		State: "pending", ExecutionStrategy: "generate", MaxRetries: 3,
	}))
	prepareAndClaimTestContext(
		t, st, testContextManifest("attempt-1", "manifest-1", "resource.closure-routing"),
	)
	manifest := testExecutionManifest()
	manifest.AttemptID = "attempt-1"
	manifest.ContextManifestID = "manifest-1"
	started, err := st.StartExecution(context.Background(), manifest)
	require.NoError(t, err)
	candidate, err := st.SubmitCandidate(context.Background(), started.ID, []CandidateFile{{
		Path: "closure.go", Content: "package closure\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(context.Background(), ExecutionTransition{
		ExecutionID: started.ID, ToStatus: execution.StatusValidating,
	})
	require.NoError(t, err)
	_, err = st.FinalizeCandidate(context.Background(), CandidateDisposition{
		ExecutionID: started.ID, Accepted: true, Reason: "accepted before closure validation",
		Resource: &Resource{
			ID: "resource.closure-routing", Kind: "asset", DeclarationHash: "decl",
			EffectiveHash: "effective", GenerationInputHash: "generation",
			DependencyContractHash: "dependency", Model: "test-model",
		},
		Files: []GeneratedFile{{
			Path: "closure.go", ResourceID: "resource.closure-routing",
			ContentHash: candidate.Files[0].ContentHash, PromptHash: started.ContextHash, Model: "test-model",
		}},
		Attempts: 1,
	})
	require.NoError(t, err)
	return st
}
