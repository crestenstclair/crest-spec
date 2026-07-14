package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/completion"
)

func TestApplyCompletionProjectionIsAtomicAndIdempotent(t *testing.T) {
	st, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	ctx := context.Background()
	snapshot := testProjectIntentSnapshot()
	require.NoError(t, st.ReconcileProjectIntent(ctx, snapshot))

	goal := completion.DeriveGoal(completion.GoalFacts{GoalID: "goal.play_notes", HasActivePlan: true})
	project := completion.DeriveProject(completion.ProjectFacts{ProjectName: snapshot.ProjectName, RequiredGoals: []completion.GoalProjection{goal}})
	source := TransitionSource{Type: "plan", ID: "plan-1", SessionID: "session-1"}
	require.NoError(t, st.ApplyCompletionProjection(ctx, snapshot.ProjectName, []completion.GoalProjection{goal}, project, source))
	require.NoError(t, st.ApplyCompletionProjection(ctx, snapshot.ProjectName, []completion.GoalProjection{goal}, project, source))

	goalHistory, err := st.ListGoalStatusHistory(ctx, goal.GoalID)
	require.NoError(t, err)
	require.Len(t, goalHistory, 1)
	require.Equal(t, "declared", goalHistory[0].FromStatus)
	require.Equal(t, "planned", goalHistory[0].ToStatus)
	require.Equal(t, "session-1", goalHistory[0].Source.SessionID)
	projectHistory, err := st.ListProjectStatusHistory(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Len(t, projectHistory, 1)

	intent, err := st.GetProjectIntent(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Equal(t, "planned", intent.CompletionStatus)
	require.NotEmpty(t, intent.CompletionReason)
}

func TestApplyCompletionProjectionRollsBackAllTransitions(t *testing.T) {
	st, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	ctx := context.Background()
	snapshot := testProjectIntentSnapshot()
	require.NoError(t, st.ReconcileProjectIntent(ctx, snapshot))

	valid := completion.GoalProjection{GoalID: "goal.play_notes", Status: completion.Planned, Reason: "planned"}
	unknown := completion.GoalProjection{GoalID: "goal.unknown", Status: completion.Planned, Reason: "planned"}
	project := completion.ProjectProjection{ProjectName: snapshot.ProjectName, Status: completion.Planned, Reason: "planned"}
	err = st.ApplyCompletionProjection(ctx, snapshot.ProjectName, []completion.GoalProjection{valid, unknown}, project, TransitionSource{Type: "test", ID: "failure"})
	require.ErrorContains(t, err, "goal.unknown")

	intent, err := st.GetProjectIntent(ctx, snapshot.ProjectName)
	require.NoError(t, err)
	require.Equal(t, "declared", intent.CompletionStatus)
	for _, goal := range intent.Goals {
		require.Equal(t, "declared", goal.Status)
	}
	history, err := st.ListGoalStatusHistory(ctx, valid.GoalID)
	require.NoError(t, err)
	require.Empty(t, history)
}
