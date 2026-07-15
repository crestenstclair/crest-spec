package store

import (
	"context"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/stretchr/testify/require"
)

func newSubmittedExecution(t *testing.T) (*Store, *ExecutionManifest, *CandidateSet) {
	t.Helper()
	st := newContextManifestStore(t, "resource.transition-safety")
	ctx := context.Background()
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-1", "manifest-1", "resource.transition-safety"),
		Dispatch: true,
	})
	require.NoError(t, err)
	started, err := st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: "synth.go", Content: "package synth\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	return st, started, candidate
}

func TestTerminalFailureRejectsSubmittedCandidateAtomically(t *testing.T) {
	ctx := context.Background()
	st, started, candidate := newSubmittedExecution(t)

	finished, err := st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID,
		ToStatus:    execution.StatusCancelled,
		Reason:      "operator cancelled generation",
	})
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusCancelled), finished.Status)

	disposed, err := st.GetCandidateSetByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, candidate.ID, disposed.ID)
	require.Equal(t, "rejected", disposed.Status)
	require.Equal(t, "operator cancelled generation", disposed.DispositionReason)
	attempt, err := st.queries.GetGenerationAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, "abandoned", attempt.Status)
}

func TestTerminalFailureRollsBackWhenCandidateIsMissing(t *testing.T) {
	ctx := context.Background()
	st, started, candidate := newSubmittedExecution(t)
	_, err := st.sqlDB.ExecContext(ctx, "DELETE FROM candidate_sets WHERE id = ?", candidate.ID)
	require.NoError(t, err)

	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID,
		ToStatus:    execution.StatusFailed,
		Reason:      "host failed",
	})
	require.ErrorContains(t, err, "reject candidate before failed")
	require.ErrorContains(t, err, "not found")

	manifest, err := st.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusCandidateSubmitted), manifest.Status,
		"the manifest transition must roll back when canonical candidate state is missing")
	attempt, err := st.queries.GetGenerationAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, "candidate_submitted", attempt.Status)
}

func TestTerminalFailureRollsBackWhenCandidateWasAlreadyDisposed(t *testing.T) {
	ctx := context.Background()
	st, started, candidate := newSubmittedExecution(t)
	_, err := st.sqlDB.ExecContext(ctx, "UPDATE candidate_sets SET status = 'accepted' WHERE id = ?", candidate.ID)
	require.NoError(t, err)

	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID,
		ToStatus:    execution.StatusTimedOut,
		Reason:      "host timeout",
	})
	require.ErrorContains(t, err, "reject candidate before timed_out")
	require.ErrorContains(t, err, "is not submitted")

	manifest, err := st.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusCandidateSubmitted), manifest.Status,
		"the manifest transition must roll back when candidate disposition cannot be written")
	attempt, err := st.queries.GetGenerationAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, "candidate_submitted", attempt.Status)
}
