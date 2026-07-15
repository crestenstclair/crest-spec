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
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		Role: string(execution.RoleFailureTriage), HostName: "spec-test", Model: "test-model",
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
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		Role: string(execution.RoleResourceImplementer), HostName: "spec-test", Model: "test-model",
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
	started, err := s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		Role: string(execution.RoleResourceImplementer), HostName: "crashed-host", Model: "test-model",
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)

	recovered, err := s.RecoverExecutions(ctx, time.Now().UTC().Add(time.Second))
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
}
