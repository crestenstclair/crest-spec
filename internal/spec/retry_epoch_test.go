package spec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

const retryEpochFailingClosureCUE = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	validations: [{kind: "custom", command: ["false"]}]
}
`

func newRestartableRetryEpochSpec(t *testing.T) (*Spec, commitTestSession, func() *Spec) {
	t.Helper()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"), []byte(withTestIntent(retryEpochFailingClosureCUE)), 0o644,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".crest-spec"), 0o755))
	databasePath := filepath.Join(root, ".crest-spec", "state.db")
	cfg := &config.Config{
		SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3, WaveMaxRetries: 2,
	}

	activeStore, err := store.New(databasePath)
	require.NoError(t, err)
	t.Cleanup(func() {
		if activeStore != nil {
			require.NoError(t, activeStore.Close())
		}
	})
	s := New(activeStore, OSFileSystem{}, cfg)
	begin, err := s.Begin(context.Background(), BeginOpts{})
	require.NoError(t, err)
	next, err := s.Next(context.Background(), begin.SessionID)
	require.NoError(t, err)
	require.NotEmpty(t, next.Resources)
	state := commitTestSession{sessionID: begin.SessionID, resourceID: next.Resources[0].ResourceID}

	restart := func() *Spec {
		t.Helper()
		require.NoError(t, activeStore.Close())
		activeStore = nil
		reopened, reopenErr := store.New(databasePath)
		require.NoError(t, reopenErr)
		activeStore = reopened
		return New(reopened, OSFileSystem{}, cfg)
	}
	return s, state, restart
}

// An accepted candidate closes one generation epoch. If a later affected-
// closure gate reopens that owner, the repair gets its own bounded retry
// budget; the executions that produced the accepted baseline cannot deadlock
// the new repair before its first context is issued.
func TestAffectedClosureReopenStartsFreshRepairRetryEpoch(t *testing.T) {
	s, state, restart := newRestartableRetryEpochSpec(t)
	ctx := context.Background()

	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, 3, resource.MaxRetries)

	prepare := func() *ContextResult {
		t.Helper()
		prepared, prepareErr := s.PrepareContext(ctx, ContextOptions{
			SessionID: state.sessionID, ResourceID: state.resourceID,
		})
		require.NoError(t, prepareErr)
		return prepared
	}
	fail := func(prepared *ContextResult, reason string) {
		t.Helper()
		assignPreparedContext(t, s, ctx, state.sessionID, state.resourceID, prepared)
		_, startErr := s.StartExecution(ctx, ExecutionStartOptions{
			AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
			IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
			HostName: "retry-epoch-test", HostSessionID: "recovery-test-host", Model: "test-model",
			TemplateHashes: prepared.TemplateHashes, SystemInstructions: prepared.SystemPrompt,
		})
		require.NoError(t, startErr)
		_, reportErr := s.ReportExecution(ctx, ExecutionReportOptions{
			AttemptID: prepared.AttemptID, Status: string(execution.StatusFailed),
			Reason: reason, Category: string(execution.FailureHostFailure),
		})
		require.NoError(t, reportErr)
	}
	assertAttemptCounts := func(total, currentEpoch int) {
		t.Helper()
		totalAttempts, countErr := s.store.CountExecutionAttempts(ctx, state.sessionID, state.resourceID)
		require.NoError(t, countErr)
		require.Equal(t, total, totalAttempts, "immutable audit count must retain prior epochs")
		epochAttempts, countErr := s.store.CountRepairEpochExecutionAttempts(
			ctx, state.sessionID, state.resourceID,
		)
		require.NoError(t, countErr)
		require.Equal(t, currentEpoch, epochAttempts)
	}

	// The first accepted candidate consumes the entire initial max_retries
	// budget: two failed executions followed by the accepted execution.
	fail(prepare(), "initial generation failed")
	fail(prepare(), "initial generation failed again")
	path := filepath.Join(filepath.Dir(s.cfg.SpecDir), "tone.go")
	accepted, err := commitTestAttempt(t, s, ctx, state.sessionID, state.resourceID,
		[]CommitFile{{Path: path, Content: "package out\n"}},
		"", "test-model")
	require.NoError(t, err)
	require.True(t, accepted.Committed)

	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, 3, resource.Attempts)
	verified := s.VerifyWave(ctx, state.sessionID, resource.WaveIndex)
	require.False(t, verified.Passed)
	require.Equal(t, 1, verified.RetryCount)
	require.Equal(t, 2, verified.MaxRetries)
	require.Equal(t, []string{state.resourceID}, verified.ReopenedResources)

	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StatePending), resource.State)
	require.Equal(t, "integration_failed", resource.Phase)
	require.Zero(t, resource.Attempts,
		"the active repair epoch starts unused even though immutable history is retained")
	assertAttemptCounts(3, 0)

	attemptsBefore, err := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, err)
	require.Len(t, attemptsBefore, 3)
	require.Equal(t, "abandoned", attemptsBefore[0].Status)
	require.Equal(t, "abandoned", attemptsBefore[1].Status)
	require.Equal(t, "accepted", attemptsBefore[2].Status)

	repair := prepare()
	require.NotEmpty(t, repair.AttemptID)
	require.NotEmpty(t, repair.HandoffID,
		"the real closure failure must route the repair without a dummy attempt")
	attemptsAfter, err := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfter, 4)
	require.Equal(t, "context_prepared", attemptsAfter[3].Status)

	// A failed repair counts in the current epoch, then a second repair may use
	// the remainder of this epoch's budget and become the new accepted baseline.
	fail(repair, "first closure repair failed")
	assertAttemptCounts(4, 1)
	secondRepair := prepare()
	fail(secondRepair, "second closure repair failed")
	assertAttemptCounts(5, 2)
	acceptedRepair, err := commitTestAttempt(t, s, ctx, state.sessionID, state.resourceID,
		[]CommitFile{{Path: path, Content: "package out\n// repaired once\n"}},
		"", "test-model")
	require.NoError(t, err)
	require.True(t, acceptedRepair.Committed)
	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	verified = s.VerifyWave(ctx, state.sessionID, resource.WaveIndex)
	require.False(t, verified.Passed)
	require.Equal(t, 2, verified.RetryCount)
	require.Equal(t, 2, verified.MaxRetries)
	require.Equal(t, []string{state.resourceID}, verified.ReopenedResources)
	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Zero(t, resource.Attempts, "the second closure reopen starts another fresh epoch")
	assertAttemptCounts(6, 0)

	// The second repair epoch is fresh and retains its own per-resource model
	// budget. Accepting its third execution closes that epoch, but cannot erase
	// the two durable closure-repair cycles already charged to this owner.
	secondEpochFirst := prepare()
	require.NotEmpty(t, secondEpochFirst.HandoffID)
	fail(secondEpochFirst, "second epoch first repair failed")
	assertAttemptCounts(7, 1)
	secondEpochSecond := prepare()
	fail(secondEpochSecond, "second epoch second repair failed")
	assertAttemptCounts(8, 2)
	acceptedSecondRepair, err := commitTestAttempt(t, s, ctx, state.sessionID, state.resourceID,
		[]CommitFile{{Path: path, Content: "package out\n// repaired twice\n"}},
		"", "test-model")
	require.NoError(t, err)
	require.True(t, acceptedSecondRepair.Committed)
	assertAttemptCounts(9, 0)

	// A third distinct accepted-baseline closure failure is durable audit
	// evidence, but WaveMaxRetries=2 means it must not reopen or dispatch the
	// owner. The errored projection survives restart and requires explicit
	// triage/spec amendment rather than another automatic model cycle.
	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	verified = s.VerifyWave(ctx, state.sessionID, resource.WaveIndex)
	require.False(t, verified.Passed)
	require.Equal(t, 3, verified.RetryCount)
	require.Equal(t, 2, verified.MaxRetries)
	require.Empty(t, verified.ReopenedResources)

	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateErrored), resource.State)
	require.Equal(t, "integration_failed", resource.Phase)
	require.Contains(t, resource.LastError, "affected-closure repair retries exhausted")

	s = restart()
	retryCount, maxRetries, statusErr := s.affectedClosureRetryStatus(ctx, state.sessionID)
	require.NoError(t, statusErr)
	require.Equal(t, 3, retryCount)
	require.Equal(t, 2, maxRetries)
	reloadedVerification := s.VerifyWave(ctx, state.sessionID, resource.WaveIndex)
	require.False(t, reloadedVerification.Passed)
	require.Equal(t, 3, reloadedVerification.RetryCount)
	require.Equal(t, 2, reloadedVerification.MaxRetries)
	require.Empty(t, reloadedVerification.ReopenedResources)
	reloadedNext, err := s.Next(ctx, state.sessionID)
	require.NoError(t, err)
	require.Len(t, reloadedNext.Resources, 1)
	require.Equal(t, StateErrored, reloadedNext.Resources[0].State)

	_, err = s.PrepareContext(ctx, ContextOptions{
		SessionID: state.sessionID, ResourceID: state.resourceID,
	})
	require.ErrorContains(t, err, "exhausted affected-closure repair retries",
		"a new accepted candidate must not reset the durable wave retry limit")
	attemptsAfterExhaustion, listErr := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, listErr)
	require.Len(t, attemptsAfterExhaustion, 9,
		"retry exhaustion must still fail before fabricating another context")
}
