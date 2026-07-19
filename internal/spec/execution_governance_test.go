package spec

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
)

func TestFailureTriageClassificationAndHumanOverrideAreAppendOnly(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: st.sessionID, ResourceID: st.resourceID, Role: string(execution.RoleFailureTriage),
	})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, st.sessionID, st.resourceID, "", "",
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		Role: string(execution.RoleFailureTriage), HostName: "spec-test", HostSessionID: lifecycle.HostSessionID, Model: "test-model",
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)

	classified, err := s.ClassifyFailure(ctx, FailureClassifyOptions{
		AttemptID: prepared.AttemptID, Category: string(execution.FailureMissingContext),
		Origin: "host", Confidence: 0.8, EvidenceSource: "triage_report",
		Evidence: "Authorization: Bearer secret-token",
	})
	require.NoError(t, err)
	require.Equal(t, "Authorization: Bearer [REDACTED]", classified.Evidence)
	require.NotEmpty(t, classified.GoalIDs, "execution goal impact should be inherited")

	override, err := s.ClassifyFailure(ctx, FailureClassifyOptions{
		AttemptID: prepared.AttemptID, Category: string(execution.FailureInvalidValidation),
		Origin: "human", Confidence: 1, EvidenceSource: "review",
		Evidence: "the check cannot observe the declared behavior", OverrideOf: classified.ID,
	})
	require.NoError(t, err)
	require.Equal(t, classified.ID, override.OverrideOf)

	rows, err := s.ListFailures(ctx, prepared.AttemptID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 2, "an override must append instead of rewriting history")
	require.Equal(t, string(execution.FailureMissingContext), rows[0].Category)
	require.Equal(t, string(execution.FailureInvalidValidation), rows[1].Category)

	require.NoError(t, s.ResolveFailure(ctx, override.ID, "validation repaired", ""))
	require.Error(t, s.ResolveFailure(ctx, override.ID, "second rewrite", ""))
}

func TestImplementationRoleCannotSubmitHostFailureClassification(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{SessionID: st.sessionID, ResourceID: st.resourceID})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, st.sessionID, st.resourceID, "", "",
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		Role: string(execution.RoleResourceImplementer), HostName: "spec-test", HostSessionID: lifecycle.HostSessionID, Model: "test-model",
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)
	_, err = s.ClassifyFailure(ctx, FailureClassifyOptions{
		AttemptID: prepared.AttemptID, Category: string(execution.FailureImplementationError),
		Origin: "host", EvidenceSource: "agent_claim",
	})
	require.ErrorContains(t, err, "cannot classify failures")
}

func TestRecoverExecutionsTimesOutAbandonedHostWithoutBlamingModel(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{SessionID: st.sessionID, ResourceID: st.resourceID})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, st.sessionID, st.resourceID, "", "",
	)
	started, err := s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		Role: string(execution.RoleResourceImplementer), HostName: "crashed-host", HostSessionID: lifecycle.HostSessionID, Model: "test-model",
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)

	recovered, err := s.RecoverExecutions(
		ctx,
		time.Time{},
		time.Now().UTC().Add(time.Second),
	)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)
	manifest, err := s.InspectExecution(ctx, started.ID, "")
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusTimedOut), manifest.Status)
	failures, err := s.ListFailures(ctx, prepared.AttemptID, 10)
	require.NoError(t, err)
	require.Len(t, failures, 1)
	require.Equal(t, string(execution.FailureHostFailure), failures[0].Category)
	require.Equal(t, "engine", failures[0].Origin)
	require.Equal(t, "execution_recovery_timeout", failures[0].EvidenceSource)
	resource, err := s.store.GetSessionResource(st.sessionID, st.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateTimedOut), resource.State)
	require.Empty(t, resource.JobID, "recovery must release the stranded worker claim")
	next, err := s.Next(ctx, st.sessionID)
	require.NoError(t, err)
	require.False(t, next.Done)
	require.Equal(t, st.resourceID, next.Resources[0].ResourceID)
}

func TestRecoverExecutionsUsesShortDispatchAndLongExecutionThresholds(t *testing.T) {
	s, st := newTestSpecWithSessionCUE(t, twoResourceSpecCUE)
	ctx := context.Background()
	next, err := s.Next(ctx, st.sessionID)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(next.Resources), 2)
	registeredResource := next.Resources[0].ResourceID
	dispatchedResource := next.Resources[1].ResourceID

	registered, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: st.sessionID, ResourceID: registeredResource,
	})
	require.NoError(t, err)
	registeredLifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, registered, st.sessionID, registeredResource, "shared-age-host", "registered-worker",
	)
	started, err := s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: registered.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: registered.ContextHash,
		Role: string(execution.RoleResourceImplementer), HostName: "shared-age-host",
		HostSessionID: registeredLifecycle.HostSessionID, Model: "test-model",
		TemplateHashes: registered.TemplateHashes, SystemInstructions: registered.SystemPrompt,
	})
	require.NoError(t, err)

	dispatched, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: st.sessionID, ResourceID: dispatchedResource,
	})
	require.NoError(t, err)
	dispatchAndAcknowledgePreparedContext(
		t, s, ctx, dispatched, st.sessionID, dispatchedResource, "shared-age-host", "pre-execution-worker",
	)

	// Both attempts are effectively the same age. The dispatch cutoff has
	// elapsed, but the registered execution's longer budget has not.
	now := time.Now().UTC()
	recovered, err := s.RecoverExecutions(
		ctx,
		now.Add(time.Second),
		now.Add(-30*time.Minute),
	)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)

	dispatchedState, err := s.store.GetSessionResource(st.sessionID, dispatchedResource)
	require.NoError(t, err)
	require.Equal(t, string(StatePending), dispatchedState.State)
	require.Equal(t, "pre_execution_recovered", dispatchedState.Phase)
	require.Empty(t, dispatchedState.JobID)
	dispatchedManifest, err := s.store.GetContextManifestByAttempt(ctx, dispatched.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "abandoned", dispatchedManifest.Attempt.Status)

	registeredManifest, err := s.InspectExecution(ctx, started.ID, "")
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusExecuting), registeredManifest.Status,
		"the dispatch timeout must never terminate an already registered execution")
	registeredState, err := s.store.GetSessionResource(st.sessionID, registeredResource)
	require.NoError(t, err)
	require.Equal(t, string(StateDispatched), registeredState.State)
	require.Equal(t, registered.AttemptID, registeredState.JobID)
}

func TestTerminalHostReportReleasesResourceForRetry(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, s, ctx, prepared, state.sessionID, state.resourceID, "", "",
	)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "failed-host", HostSessionID: lifecycle.HostSessionID, Model: "test-model",
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)

	_, err = s.ReportExecution(ctx, ExecutionReportOptions{
		AttemptID: prepared.AttemptID, Status: string(execution.StatusFailed),
		Reason:   "worker process exited before candidate submission",
		Category: string(execution.FailureHostFailure),
	})
	require.NoError(t, err)
	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateErrored), resource.State)
	require.Empty(t, resource.JobID)
	require.Equal(t, 1, resource.Attempts)

	retry, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	require.NotEqual(t, prepared.AttemptID, retry.AttemptID)
}
