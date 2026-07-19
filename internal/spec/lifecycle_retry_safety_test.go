package spec

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func TestPrepareContextRejectsExhaustedExecutionBudgetBeforeDispatch(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()

	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Positive(t, resource.MaxRetries)
	for attempt := 0; attempt < resource.MaxRetries; attempt++ {
		prepared, prepareErr := s.PrepareContext(ctx, ContextOptions{
			SessionID: state.sessionID, ResourceID: state.resourceID,
		})
		require.NoError(t, prepareErr)
		assignPreparedContext(t, s, ctx, state.sessionID, state.resourceID, prepared)
		_, startErr := s.StartExecution(ctx, ExecutionStartOptions{
			AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
			IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
			HostName: "retry-test", HostSessionID: "recovery-test-host", Model: "test-model",
			TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
		})
		require.NoError(t, startErr)
		_, reportErr := s.ReportExecution(ctx, ExecutionReportOptions{
			AttemptID: prepared.AttemptID, Status: string(execution.StatusFailed),
			Reason: "host execution failed", Category: string(execution.FailureHostFailure),
		})
		require.NoError(t, reportErr)
	}

	_, err = s.PrepareContext(ctx, ContextOptions{
		SessionID:  state.sessionID,
		ResourceID: state.resourceID,
	})
	require.ErrorContains(t, err, "exhausted max retries")

	attempts, listErr := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, listErr)
	require.Len(t, attempts, resource.MaxRetries,
		"retry exhaustion must fail before creating another context attempt")

	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateErrored), resource.State)
	require.Equal(t, resource.MaxRetries, resource.Attempts)
}

func TestPrepareContextDoesNotCountAbandonedPreExecutionContextsAsRetries(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()

	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Positive(t, resource.MaxRetries)
	for attempt := 0; attempt < resource.MaxRetries; attempt++ {
		prepared, prepareErr := s.PrepareContext(ctx, ContextOptions{
			SessionID:  state.sessionID,
			ResourceID: state.resourceID,
		})
		require.NoError(t, prepareErr)
		recovered, recoverErr := s.store.AbandonPreparedContext(ctx, store.PreparedContextAbandonment{
			AttemptID: prepared.AttemptID, Reason: "context was prepared but no worker was assigned",
		})
		require.NoError(t, recoverErr)
		require.True(t, recovered.Recovered)
	}

	replacement, err := s.PrepareContext(ctx, ContextOptions{
		SessionID:  state.sessionID,
		ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, replacement.AttemptID)

	attempts, listErr := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, listErr)
	require.Len(t, attempts, resource.MaxRetries+1)
	used, countErr := s.store.CountExecutionAttempts(ctx, state.sessionID, state.resourceID)
	require.NoError(t, countErr)
	require.Zero(t, used)
}

func TestResolveCannotReopenCommittedResourceWithoutValidationBatch(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	commitResource(t, s, state.sessionID, state.resourceID)

	err := s.Resolve(ctx, state.sessionID, state.resourceID, "try it again", "")
	require.ErrorContains(t, err, "is committed")

	resource, getErr := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, getErr)
	require.Equal(t, string(StateCommitted), resource.State)
}

func TestValidationBatchMayReopenCommittedOwner(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	commitResource(t, s, state.sessionID, state.resourceID)

	reopened, err := s.store.ReopenSessionResources(
		ctx, state.sessionID, []string{state.resourceID}, "compiler diagnostic names this owner",
	)
	require.NoError(t, err)
	require.Equal(t, 1, reopened)

	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StatePending), resource.State)
}
