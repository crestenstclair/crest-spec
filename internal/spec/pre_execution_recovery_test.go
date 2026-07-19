package spec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

func assignPreparedContext(
	t *testing.T,
	s *Spec,
	ctx context.Context,
	sessionID string,
	resourceID string,
	prepared *ContextResult,
) {
	t.Helper()
	const (
		hostSessionID = "recovery-test-host"
		workerID      = "recovery-test-worker"
	)
	_, err := s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: prepared.DispatchID,
		SessionID: sessionID, ResourceID: resourceID,
		HostSessionID: hostSessionID, WorkerID: workerID,
	})
	require.NoError(t, err)
	_, err = s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "worker_acknowledged", DispatchID: prepared.DispatchID,
		SessionID: sessionID, ResourceID: resourceID,
		HostSessionID: hostSessionID, WorkerID: workerID,
	})
	require.NoError(t, err)
}

func TestDispatchFailedImmediatelyReleasesClaimAndForcesExecutionContextRebuild(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	const (
		hostSessionID = "failed-dispatch-host"
		workerID      = "failed-dispatch-worker"
	)
	_, err = s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: prepared.DispatchID,
		SessionID: state.sessionID, ResourceID: state.resourceID,
		HostSessionID: hostSessionID, WorkerID: workerID,
	})
	require.NoError(t, err)
	failed, err := s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_failed", DispatchID: prepared.DispatchID,
		SessionID: state.sessionID, ResourceID: state.resourceID,
		HostSessionID: hostSessionID, WorkerID: workerID,
		Reason: "native Codex worker could not be started",
	})
	require.NoError(t, err)
	require.Equal(t, prepared.AttemptID, failed.AttemptID)

	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StatePending), resource.State)
	require.Empty(t, resource.JobID)
	require.Zero(t, resource.Attempts)
	_, err = s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_failed", DispatchID: prepared.DispatchID,
		SessionID: state.sessionID, ResourceID: state.resourceID,
		HostSessionID: hostSessionID, WorkerID: workerID,
		Reason: "replay after response loss",
	})
	require.NoError(t, err)

	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "spec-test", HostSessionID: hostSessionID, Model: "test-model",
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.ErrorContains(t, err, "corrective_action=rebuild_context")
	used, err := s.store.CountExecutionAttempts(ctx, state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Zero(t, used)
	replacement, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	require.NotEqual(t, prepared.AttemptID, replacement.AttemptID)
}

func TestStartExecutionInvalidatesChangedPlanContextWithRebuildToken(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	assignPreparedContext(t, s, ctx, state.sessionID, state.resourceID, prepared)

	specPath := filepath.Join(s.cfg.SpecDir, "spec.cue")
	contents, err := os.ReadFile(specPath)
	require.NoError(t, err)
	changed := strings.Replace(string(contents), `from: "f64"`, `from: "string"`, 1)
	require.NotEqual(t, string(contents), changed)
	require.NoError(t, os.WriteFile(specPath, []byte(changed), 0o644))

	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "spec-test", HostSessionID: "recovery-test-host", Model: "test-model",
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.ErrorContains(t, err, "corrective_action=rebuild_context")
	require.ErrorContains(t, err, "prepared attempt was abandoned")

	manifest, err := s.store.GetContextManifestByAttempt(ctx, prepared.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "abandoned", manifest.Attempt.Status)
	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StatePending), resource.State)
	require.Empty(t, resource.JobID)
	require.Zero(t, resource.Attempts)
	_, err = s.store.GetExecutionManifestByAttempt(ctx, prepared.AttemptID)
	require.Error(t, err, "source invalidation must happen before execution registration")
	failures, err := s.store.ListFailureClassificationsByAttempt(ctx, prepared.AttemptID)
	require.NoError(t, err)
	require.Len(t, failures, 1)
	require.Equal(t, string(execution.FailureStaleContext), failures[0].Category)
	require.Equal(t, "execution_start_plan_inputs_changed", failures[0].EvidenceSource)
	require.Equal(t, "rebuild_context", failures[0].CorrectiveAction)
	used, err := s.store.CountExecutionAttempts(ctx, state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Zero(t, used)
}

func TestNextRecoversStalePreparedClaimBeforeWaveSelection(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	assignPreparedContext(t, s, ctx, state.sessionID, state.resourceID, prepared)

	s.cfg.DispatchTimeout = time.Nanosecond
	s.cfg.ExecutionTimeout = 30 * time.Minute
	next, err := s.Next(ctx, state.sessionID)
	require.NoError(t, err)
	require.False(t, next.Done)
	require.NotEmpty(t, next.Resources)
	require.Equal(t, state.resourceID, next.Resources[0].ResourceID)
	require.Equal(t, StatePending, next.Resources[0].State)

	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StatePending), resource.State)
	require.Equal(t, "pre_execution_recovered", resource.Phase)
	require.Empty(t, resource.JobID)
	require.Zero(t, resource.Attempts)
	manifest, err := s.store.GetContextManifestByAttempt(ctx, prepared.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "abandoned", manifest.Attempt.Status)

	// The recovered context is provenance, not a retry. Preparing replacement
	// context remains legal even when max_retries is one.
	resource.MaxRetries = 1
	require.NoError(t, s.store.UpsertSessionResource(*resource))
	replacement, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	require.NotEqual(t, prepared.AttemptID, replacement.AttemptID)
	require.Equal(t, 2, mustContextManifest(t, s, ctx, replacement.AttemptID).Attempt.RetryNumber,
		"immutable attempt ordinal advances even though retry budget does not")
}

func TestCommitTimeSourceInvalidationIsStaleContextAndRetryExempt(t *testing.T) {
	s, state := newTestSpecWithSession(t)
	ctx := context.Background()
	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	resource.MaxRetries = 1
	require.NoError(t, s.store.UpsertSessionResource(*resource))

	prepared, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err)
	assignPreparedContext(t, s, ctx, state.sessionID, state.resourceID, prepared)
	_, err = s.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "spec-test", HostSessionID: "recovery-test-host", Model: "test-model",
		TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)

	specPath := filepath.Join(s.cfg.SpecDir, "spec.cue")
	original, err := os.ReadFile(specPath)
	require.NoError(t, err)
	changed := strings.Replace(string(original), `from: "f64"`, `from: "string"`, 1)
	require.NotEqual(t, string(original), changed)
	require.NoError(t, os.WriteFile(specPath, []byte(changed), 0o644))

	result, err := s.CommitAttempt(ctx, prepared.AttemptID, []CommitFile{{
		Path: "generated/stale_context.go", Content: "package generated\n",
	}}, "", CommitMetadata{})
	require.Nil(t, result)
	require.ErrorContains(t, err, "corrective_action=rebuild_context")
	executionManifest, err := s.store.GetExecutionManifestByAttempt(ctx, prepared.AttemptID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusFailed), executionManifest.Status)
	failures, err := s.store.ListFailureClassificationsByAttempt(ctx, prepared.AttemptID)
	require.NoError(t, err)
	require.Len(t, failures, 1)
	require.Equal(t, string(execution.FailureStaleContext), failures[0].Category)
	require.Equal(t, "rebuild_context", failures[0].CorrectiveAction)
	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateErrored), resource.State)
	require.Empty(t, resource.JobID)
	require.Zero(t, resource.Attempts)
	used, err := s.store.CountExecutionAttempts(ctx, state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Zero(t, used, "engine-invalidated stale context must not consume model retry budget")

	require.NoError(t, os.WriteFile(specPath, original, 0o644))
	replacement, err := s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.NoError(t, err, "replacement context remains legal at max_retries=1")
	require.NotEqual(t, prepared.AttemptID, replacement.AttemptID)
}

func mustContextManifest(t *testing.T, s *Spec, ctx context.Context, attemptID string) *store.ContextManifest {
	t.Helper()
	manifest, err := s.store.GetContextManifestByAttempt(ctx, attemptID)
	require.NoError(t, err)
	return manifest
}
