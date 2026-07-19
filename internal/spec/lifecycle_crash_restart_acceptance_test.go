package spec

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// TestCrashRestartAndValidationInfrastructureRetryPreserveAcceptedSource is a
// session-level acceptance test for two recovery boundaries that previously
// interacted catastrophically in a real apply:
//
//  1. the host exits after dispatch_requested but before worker acknowledgement
//     or execution registration; and
//  2. the final closure gate cannot launch one of its declared executables.
//
// The test deliberately uses a file-backed SQLite store and reconstructs Spec
// after closing it. The recovered pre-execution context is provenance, not a
// model retry. Once source is accepted, an infrastructure-only validation
// failure must retry the gate directly and must never redispatch source work.
func TestCrashRestartAndValidationInfrastructureRetryPreserveAcceptedSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses an executable POSIX shell fixture")
	}

	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	stateDir := filepath.Join(root, ".crest-spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"),
		[]byte(withTestIntent(commitTestSpecCUE+`
project: validations: [{
	kind: "custom"
	command: ["crest-transient-validator"]
	description: "transient host validation infrastructure is available"
}]
`)),
		0o644,
	))

	validatorBin := filepath.Join(root, "validator-bin")
	require.NoError(t, os.MkdirAll(validatorBin, 0o755))
	t.Setenv("PATH", validatorBin)

	statePath := filepath.Join(stateDir, "state.db")
	openSpec := func(dispatchTimeout time.Duration) (*Spec, *store.Store) {
		t.Helper()
		st, err := store.New(statePath)
		require.NoError(t, err)
		return New(st, OSFileSystem{}, &config.Config{
			SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3,
			WaveMaxRetries: 2, DispatchTimeout: dispatchTimeout, ExecutionTimeout: 30 * time.Minute,
		}), st
	}

	initial, initialStore := openSpec(30 * time.Minute)
	begin, err := initial.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.Len(t, begin.Plan, 1)
	resourceID := begin.Plan[0].ResourceID

	batch, err := initial.PrepareWaveContexts(ctx, WaveContextOptions{
		SessionID: begin.SessionID, Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, batch.Contexts, 1)
	orphan := batch.Contexts[0].Context
	require.NotNil(t, orphan)
	require.False(t, orphan.Blocked)
	require.False(t, orphan.NoDispatch)

	const (
		orphanHost   = "host-before-crash"
		orphanWorker = "worker-before-crash"
	)
	requested, err := initial.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: orphan.DispatchID,
		SessionID: begin.SessionID, ResourceID: resourceID,
		HostSessionID: orphanHost, WorkerID: orphanWorker,
	})
	require.NoError(t, err)
	require.NotNil(t, requested.DispatchRequestedAt)
	require.Nil(t, requested.WorkerAcknowledgedAt)
	require.Nil(t, requested.ExecutionRegisteredAt)
	claimed, err := initialStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateDispatched), claimed.State)
	require.Equal(t, orphan.AttemptID, claimed.JobID)
	require.Zero(t, claimed.Attempts)

	// Simulate an abrupt MCP/host process exit: only durable SQLite state and
	// repository bytes survive into the replacement Spec instance.
	require.NoError(t, initialStore.Close())
	restarted, restartedStore := openSpec(time.Nanosecond)
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })

	next, err := restarted.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.False(t, next.Done)
	require.Len(t, next.Resources, 1)
	require.Equal(t, resourceID, next.Resources[0].ResourceID)
	require.Equal(t, StatePending, next.Resources[0].State)

	recovered, err := restartedStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StatePending), recovered.State)
	require.Equal(t, "pre_execution_recovered", recovered.Phase)
	require.Empty(t, recovered.JobID)
	require.Zero(t, recovered.Attempts,
		"a host crash before execution registration must not consume a generation retry")
	orphanManifest, err := restartedStore.GetContextManifestByAttempt(ctx, orphan.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "abandoned", orphanManifest.Attempt.Status)
	orphanFailures, err := restartedStore.ListFailureClassificationsByAttempt(ctx, orphan.AttemptID)
	require.NoError(t, err)
	require.Len(t, orphanFailures, 1)
	require.Equal(t, string(execution.FailureStaleContext), orphanFailures[0].Category)
	require.Equal(t, store.PreExecutionRecoveryEvidenceSource, orphanFailures[0].EvidenceSource)
	used, err := restartedStore.CountExecutionAttempts(ctx, begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Zero(t, used)

	replacementBatch, err := restarted.PrepareWaveContexts(ctx, WaveContextOptions{
		SessionID: begin.SessionID, Limit: 1,
	})
	require.NoError(t, err)
	require.Len(t, replacementBatch.Contexts, 1)
	replacement := replacementBatch.Contexts[0].Context
	require.NotNil(t, replacement)
	require.NotEqual(t, orphan.AttemptID, replacement.AttemptID)
	replacementManifest, err := restartedStore.GetContextManifestByAttempt(ctx, replacement.AttemptID)
	require.NoError(t, err)
	require.Equal(t, 2, replacementManifest.Attempt.RetryNumber,
		"immutable attempt history advances even though model retry accounting does not")

	const (
		restartHost   = "host-after-crash"
		restartWorker = "worker-after-crash"
	)
	dispatchAndAcknowledgePreparedContext(
		t, restarted, ctx, replacement, begin.SessionID, resourceID, restartHost, restartWorker,
	)
	_, err = restarted.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: replacement.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: replacement.ContextHash,
		HostName: "spec-acceptance-test", HostVersion: "1", Provider: "test",
		HostSessionID: restartHost, Model: "test-model",
		TemplateHashes: replacement.TemplateHashes, SystemInstructions: replacement.SystemPrompt,
	})
	require.NoError(t, err)
	const acceptedSource = "package generated\n\nconst Accepted = true\n"
	commit, err := restarted.CommitAttempt(ctx, replacement.AttemptID, []CommitFile{{
		Path: "src/accepted_source.go", Content: acceptedSource,
	}}, "accepted after crash recovery", CommitMetadata{})
	require.NoError(t, err)
	require.True(t, commit.Committed)
	require.True(t, commit.ValidationDeferred)

	acceptedResource, err := restartedStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), acceptedResource.State)
	require.Equal(t, 1, acceptedResource.Attempts)
	attemptsBeforeGate, err := restartedStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	lifecyclesBeforeGate, err := restartedStore.ListDispatchLifecyclesBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	workspaceBeforeGate, err := os.ReadFile(filepath.Join(root, "src", "accepted_source.go"))
	require.NoError(t, err)
	require.Equal(t, acceptedSource, string(workspaceBeforeGate))

	firstGate := restarted.VerifyWave(ctx, begin.SessionID, acceptedResource.WaveIndex)
	require.False(t, firstGate.Passed, "an unavailable validation executable must fail closed")
	require.Contains(t, joinedCrashRestartWaveErrors(firstGate.Errors), "crest-transient-validator")
	require.Contains(t, joinedCrashRestartWaveErrors(firstGate.Errors), "could not run")
	require.Empty(t, firstGate.ReopenedResources,
		"validation infrastructure failure must not turn accepted source into model work")

	preserved, err := restartedStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), preserved.State)
	require.Equal(t, acceptedResource.Attempts, preserved.Attempts)
	attemptsAfterFailedGate, err := restartedStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfterFailedGate, len(attemptsBeforeGate),
		"an infrastructure-only gate failure must not create generation attempts")
	lifecyclesAfterFailedGate, err := restartedStore.ListDispatchLifecyclesBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, lifecyclesAfterFailedGate, len(lifecyclesBeforeGate),
		"an infrastructure-only gate failure must not create worker dispatches")

	validationRetry, err := restarted.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.True(t, validationRetry.ValidationReady)
	require.Empty(t, validationRetry.Resources)
	retryBatch, err := restarted.PrepareWaveContexts(ctx, WaveContextOptions{
		SessionID: begin.SessionID, Limit: 1,
	})
	require.NoError(t, err)
	require.True(t, retryBatch.ValidationReady)
	require.Empty(t, retryBatch.Contexts,
		"validation retry must return directly to the gate without source redispatch")

	writeCrashRestartExecutable(t, filepath.Join(validatorBin, "crest-transient-validator"))
	secondGate := restarted.VerifyWave(ctx, begin.SessionID, retryBatch.ValidationWaveIndex)
	require.True(t, secondGate.Passed)
	require.Empty(t, secondGate.ReopenedResources)
	verifiedResource, err := restartedStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), verifiedResource.State)
	require.Equal(t, "verified", verifiedResource.Phase)
	require.Equal(t, acceptedResource.Attempts, verifiedResource.Attempts)
	workspaceAfterGate, err := os.ReadFile(filepath.Join(root, "src", "accepted_source.go"))
	require.NoError(t, err)
	require.Equal(t, workspaceBeforeGate, workspaceAfterGate)

	finished, err := restarted.Finish(ctx, begin.SessionID, false)
	require.NoError(t, err)
	require.False(t, finished.Blocked)
	require.Equal(t, 1, finished.Committed)
}

func writeCrashRestartExecutable(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
}

func joinedCrashRestartWaveErrors(errors []WaveError) string {
	joined := ""
	for _, waveError := range errors {
		joined += waveError.Kind + ": " + waveError.Message + "\n"
	}
	return joined
}
