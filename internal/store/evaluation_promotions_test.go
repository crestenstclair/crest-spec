package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func completeWinningEvaluationRun(t *testing.T, st *Store) *EvaluationRun {
	t.Helper()
	ctx := context.Background()
	run, baselineID, _ := evaluationRunFixture(t, st, true)
	for index := range run.Assignments {
		claim, err := st.ClaimEvaluationAssignment(ctx, run.ID, "promotion-worker", "", time.Minute)
		require.NoError(t, err)
		value := 1.0
		if claim.Assignment.ConfigurationID == baselineID {
			value = 0
		}
		_, err = st.SubmitEvaluationAssignment(ctx, claim.Assignment.ID, claim.LeaseID, "promotion-worker", claim.LeaseToken, EvaluationAssignmentResult{
			AttemptID: completeEvaluationAttempt(t, st, claim, index), TerminalStatus: "completed", Metrics: primaryEvaluationMetrics(value),
		})
		require.NoError(t, err)
	}
	completed, err := st.FinalizeEvaluationRun(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, "candidate_wins", completed.Conclusion)
	return completed
}

func TestEvaluationPromotionRequiresWinningEvidenceAndRecordsHumanHistory(t *testing.T) {
	st := newContextManifestStore(t, "aggregate.Engine.Voice")
	ctx := context.Background()
	run := completeWinningEvaluationRun(t, st)
	change := map[string]any{
		"learning_ids": []any{"learning-a", "learning-b"},
		"markdown":     "- preserve behavioral provenance",
	}
	proposal, err := st.CreateEvaluationPromotionProposal(
		ctx, run.ID, "candidate", "learning", "templates/learned/rust.md", change, "template-before-hash",
	)
	require.NoError(t, err)
	assert.Equal(t, "eligible", proposal.Status)
	assert.Contains(t, proposal.EligibilityReason, "unique evaluated winner")
	assert.Equal(t, "candidate", proposal.ConfigurationName)
	assert.NotEmpty(t, proposal.ChangeHash)
	assert.Equal(t, "template-before-hash", proposal.RollbackIdentity)

	duplicate, err := st.CreateEvaluationPromotionProposal(
		ctx, run.ID, "candidate", "learning", "templates/learned/rust.md", change, "template-before-hash",
	)
	require.NoError(t, err)
	assert.Equal(t, proposal.ID, duplicate.ID)

	approved, err := st.RecordEvaluationPromotionDecision(ctx, proposal.ID, "approved", "human@example.test", "held-out evidence is convincing")
	require.NoError(t, err)
	assert.Equal(t, "approved", approved.Status)
	require.Len(t, approved.Decisions, 1)

	applied, err := st.RecordEvaluationPromotionDecision(ctx, proposal.ID, "applied", "crest-spec", "exact proposed change applied")
	require.NoError(t, err)
	assert.Equal(t, "applied", applied.Status)

	rolledBack, err := st.RecordEvaluationPromotionDecision(ctx, proposal.ID, "rolled_back", "human@example.test", "rollback identity restored")
	require.NoError(t, err)
	assert.Equal(t, "rolled_back", rolledBack.Status)
	require.Len(t, rolledBack.Decisions, 3)
	assert.Equal(t, []string{"approved", "applied", "rolled_back"}, []string{
		rolledBack.Decisions[0].Decision, rolledBack.Decisions[1].Decision, rolledBack.Decisions[2].Decision,
	})
}

func TestEvaluationPromotionCannotApproveIncompleteEvidence(t *testing.T) {
	st := newContextManifestStore(t, "aggregate.Engine.Voice")
	ctx := context.Background()
	run, _, _ := evaluationRunFixture(t, st, true)
	proposal, err := st.CreateEvaluationPromotionProposal(ctx, run.ID, "candidate", "template", "task-template-v2", map[string]any{
		"template_hash": "task-template-v2",
	}, "task-template-v1")
	require.NoError(t, err)
	assert.Equal(t, "proposed", proposal.Status)
	assert.Contains(t, proposal.EligibilityReason, "not complete")

	_, err = st.RecordEvaluationPromotionDecision(ctx, proposal.ID, "approved", "human@example.test", "trying to bypass the gate")
	require.Error(t, err)
	rejected, err := st.RecordEvaluationPromotionDecision(ctx, proposal.ID, "rejected", "human@example.test", "evaluation must finish first")
	require.NoError(t, err)
	assert.Equal(t, "rejected", rejected.Status)
	require.Len(t, rejected.Decisions, 1)
}
