package plan

import (
	"testing"

	"github.com/stretchr/testify/require"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

func TestGoalImpactAnnotationsAndSlicesAreDeterministic(t *testing.T) {
	project := &cuepkg.Project{
		Name:  "example",
		Goals: map[string]cuepkg.Goal{"ship": {Priority: "required", Capabilities: []string{"capability.run"}}},
		Capabilities: map[string]cuepkg.Capability{"run": {
			Goals: []string{"goal.ship"},
			Acceptance: map[string]cuepkg.AcceptanceScenario{
				"works": {Description: "the example runs", Evidence: []string{"evidence.run"}},
			},
		}},
		Contexts: map[string]cuepkg.Context{"Example": {
			Purpose: "run it",
			ApplicationServices: map[string]cuepkg.ApplicationService{
				"Runner": {ContributesTo: []cuepkg.Contribution{{Capability: "capability.run", Contribution: "coordinates execution"}}},
			},
		}},
	}
	registry, err := cuepkg.NewRegistry(project)
	require.NoError(t, err)
	actions := []PlannedAction{{ResourceID: "applicationService.Example.Runner", Kind: ActionCreate, Reason: "new resource"}}
	annotateGoalImpact(actions, registry)

	action := actions[0]
	require.NotEmpty(t, action.OperationID)
	require.Equal(t, "structural", action.Category)
	require.Equal(t, []string{"capability.run"}, action.Capabilities)
	require.Equal(t, []string{"goal.ship"}, action.Goals)
	require.Equal(t, []string{"the example runs"}, action.ExpectedBehavior)
	require.Equal(t, []string{"evidence.run"}, action.ExpectedEvidence)
	require.False(t, action.SharedInfrastructure)

	slices := BuildCapabilitySlices(actions, registry)
	require.Len(t, slices, 1)
	require.True(t, slices[0].Required)
	require.Equal(t, action.OperationID, slices[0].OperationIDs[0])
	// Identical inputs must produce byte-stable identifiers and ordering.
	second := []PlannedAction{{ResourceID: action.ResourceID, Kind: action.Kind, Reason: action.Reason}}
	annotateGoalImpact(second, registry)
	require.Equal(t, action.OperationID, second[0].OperationID)
}

func TestUnmappedOperationIsExplicitSharedInfrastructure(t *testing.T) {
	registry, err := cuepkg.NewRegistry(&cuepkg.Project{Contexts: map[string]cuepkg.Context{"Shared": {Purpose: "shared", ValueObjects: map[string]cuepkg.ValueObject{"Clock": {}}}}})
	require.NoError(t, err)
	actions := []PlannedAction{{ResourceID: "valueObject.Shared.Clock", Kind: ActionModify, Reason: "declaration changed"}}
	annotateGoalImpact(actions, registry)
	require.True(t, actions[0].SharedInfrastructure)
	require.Equal(t, "corrective", actions[0].Category)
}
