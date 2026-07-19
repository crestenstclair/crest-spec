package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
)

func TestClosureFailureRoutingAtomicallyDowngradesCommittedFastPaths(t *testing.T) {
	for _, strategy := range []string{"validate_existing", "reuse_candidate"} {
		t.Run(strategy, func(t *testing.T) {
			ctx := context.Background()
			st := newAcceptedClosureRoutingStore(t, ":memory:")
			sourceCandidateID := ""
			if strategy == "reuse_candidate" {
				candidate, err := st.GetCandidateSetByAttempt(ctx, "attempt-1")
				require.NoError(t, err)
				sourceCandidateID = candidate.ID
			}
			require.NoError(t, st.UpsertSessionResource(SessionResource{
				SessionID: "session-1", ResourceID: "resource.fast", State: "committed",
				Phase: "integration_pending", ExecutionStrategy: strategy,
				SourceCandidateID: sourceCandidateID, MaxRetries: 3,
			}))

			result, err := st.RouteClosureValidationFailures(ctx, ClosureValidationFailureRoutingInput{
				SessionID: "session-1", ResourceIDs: []string{"resource.fast"},
				Reason: "closure compiler failure", Category: execution.FailureIntegrationError,
			})
			require.NoError(t, err)
			require.Equal(t, []string{"resource.fast"}, result.ReopenedResources)
			require.Empty(t, result.Failures, "a zero-execution fast path has no truthful source attempt to classify")
			require.Empty(t, result.Handoffs, "a zero-execution fast path has no truthful source attempt to hand off")
			require.Empty(t, result.RetryCounts)

			fast, err := st.GetSessionResource("session-1", "resource.fast")
			require.NoError(t, err)
			require.Equal(t, "pending", fast.State)
			require.Equal(t, "integration_failed", fast.Phase)
			require.Equal(t, "generate", fast.ExecutionStrategy)
			require.Empty(t, fast.SourceCandidateID)
			require.Zero(t, fast.Attempts)
			require.Equal(t, "closure compiler failure", fast.LastError)

			generated, err := st.GetSessionResource("session-1", "resource.closure-routing")
			require.NoError(t, err)
			require.Equal(t, "committed", generated.State, "unselected owners must not change")
		})
	}
}

func TestClosureFailureRoutingRollsBackMixedGeneratedAndFastPathBatch(t *testing.T) {
	ctx := context.Background()
	st := newAcceptedClosureRoutingStore(t, ":memory:")
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: "resource.fast", State: "committed",
		Phase: "integration_pending", ExecutionStrategy: "validate_existing", MaxRetries: 3,
	}))

	result, err := st.routeClosureValidationFailures(ctx, ClosureValidationFailureRoutingInput{
		SessionID:   "session-1",
		ResourceIDs: []string{"resource.closure-routing", "resource.fast"},
		Reason:      "closure compiler failure", Category: execution.FailureIntegrationError,
		transitionHook: func(stage, resourceID string) error {
			if stage == "after_reopen" && resourceID == "resource.fast" {
				return errors.New("simulated process interruption")
			}
			return nil
		},
	})
	require.ErrorContains(t, err, "simulated process interruption")
	require.Nil(t, result)
	assertClosureRoutingProjection(t, st, "committed", "committed", 0, 0)
}

func TestClosureFailureRoutingPublishesMixedBatchInOneCommit(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "state.db")
	writer := newAcceptedClosureRoutingStore(t, databasePath)
	require.NoError(t, writer.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: "resource.fast", State: "committed",
		Phase: "integration_pending", ExecutionStrategy: "validate_existing", MaxRetries: 3,
	}))
	reader, err := New(databasePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })

	reachedUncommittedBatch := make(chan struct{})
	releaseCommit := make(chan struct{})
	resultChannel := make(chan *ClosureValidationFailureRoutingResult, 1)
	errorChannel := make(chan error, 1)
	go func() {
		result, routeErr := writer.routeClosureValidationFailures(ctx, ClosureValidationFailureRoutingInput{
			SessionID:   "session-1",
			ResourceIDs: []string{"resource.closure-routing", "resource.fast"},
			Reason:      "closure compiler failure", Category: execution.FailureIntegrationError,
			transitionHook: func(stage, resourceID string) error {
				if stage == "after_reopen" && resourceID == "resource.fast" {
					close(reachedUncommittedBatch)
					<-releaseCommit
				}
				return nil
			},
		})
		resultChannel <- result
		errorChannel <- routeErr
	}()
	select {
	case <-reachedUncommittedBatch:
	case <-time.After(5 * time.Second):
		t.Fatal("routing did not reach the uncommitted mixed-owner boundary")
	}

	assertClosureRoutingProjection(t, reader, "committed", "committed", 0, 0)
	close(releaseCommit)
	result := <-resultChannel
	require.NoError(t, <-errorChannel)
	require.ElementsMatch(t,
		[]string{"resource.closure-routing", "resource.fast"}, result.ReopenedResources)
	assertClosureRoutingProjection(t, reader, "pending", "pending", 1, 1)
}

func assertClosureRoutingProjection(
	t *testing.T,
	st *Store,
	generatedState, fastState string,
	wantFailures, wantHandoffs int,
) {
	t.Helper()
	ctx := context.Background()
	generated, err := st.GetSessionResource("session-1", "resource.closure-routing")
	require.NoError(t, err)
	require.Equal(t, generatedState, generated.State)
	fast, err := st.GetSessionResource("session-1", "resource.fast")
	require.NoError(t, err)
	require.Equal(t, fastState, fast.State)
	failures, err := st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Len(t, failures, wantFailures)
	handoffs, err := st.ListAttemptHandoffsBySource(ctx, "attempt-1")
	require.NoError(t, err)
	require.Len(t, handoffs, wantHandoffs)
}
