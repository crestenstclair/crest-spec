package completion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveGoalLifecycle(t *testing.T) {
	complete := GoalFacts{GoalID: "goal.play", DependenciesComplete: true, AcceptanceVerified: true, RequiredEvidenceCurrent: true, RequiredGoalChecksPass: true}
	tests := []struct {
		name  string
		facts GoalFacts
		want  Status
	}{
		{"declared", GoalFacts{GoalID: "g"}, Declared},
		{"planned", GoalFacts{GoalID: "g", HasActivePlan: true}, Planned},
		{"partial", GoalFacts{GoalID: "g", ContributionCount: 2, AcceptedContributions: 1}, PartiallyImplemented},
		{"local", GoalFacts{GoalID: "g", ContributionCount: 2, AcceptedContributions: 2, AllContributionsLocal: true}, LocallyValidated},
		{"integrated", GoalFacts{GoalID: "g", ContributionCount: 2, AcceptedContributions: 2, AllContributionsLocal: true, AllContributionsIntegrated: true}, Integrated},
		{"behavioral", GoalFacts{GoalID: "g", AcceptanceVerified: true, RequiredEvidenceCurrent: true}, BehaviorallyVerified},
		{"complete", complete, Complete},
		{"blocked wins", GoalFacts{GoalID: "g", DependenciesComplete: true, AcceptanceVerified: true, RequiredEvidenceCurrent: true, RequiredGoalChecksPass: true, Blockers: []Blocker{{Category: "missing_context", Reason: "contract omitted"}}}, Blocked},
		{"regressed wins", GoalFacts{GoalID: "g", PreviouslyComplete: true, ReverificationRequired: true, RequiredEvidenceCurrent: false, Blockers: []Blocker{{Category: "tool_failure", Reason: "runner unavailable"}}}, Regressed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { assert.Equal(t, tt.want, DeriveGoal(tt.facts).Status) })
	}
}

func TestDeriveProjectUsesRequiredGoalsAndChecks(t *testing.T) {
	goal := func(id string, status Status) GoalProjection { return GoalProjection{GoalID: id, Status: status} }

	projection := DeriveProject(ProjectFacts{ProjectName: "synth", ProjectChecksPass: true, RequiredGoals: []GoalProjection{goal("goal.a", Complete), goal("goal.b", Complete)}})
	require.Equal(t, Complete, projection.Status)

	projection = DeriveProject(ProjectFacts{ProjectName: "synth", ProjectChecksPass: false, RequiredGoals: []GoalProjection{goal("goal.a", Complete)}})
	require.Equal(t, BehaviorallyVerified, projection.Status)
	require.Empty(t, projection.MissingGoals)

	projection = DeriveProject(ProjectFacts{ProjectName: "synth", RequiredGoals: []GoalProjection{goal("goal.a", Complete), goal("goal.b", Declared)}})
	require.Equal(t, PartiallyImplemented, projection.Status)
	require.Equal(t, []string{"goal.b"}, projection.MissingGoals)
}
