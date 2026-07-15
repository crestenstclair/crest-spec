package spec

import (
	"testing"

	completionpkg "github.com/crestenstclair/crest-spec/internal/completion"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/require"
)

func TestCompletionProjectionPureDerivation(t *testing.T) {
	tests := []struct {
		name        string
		arrange     func(*completionProjectionFacts)
		wantGoal    completionpkg.Status
		wantProject completionpkg.Status
	}{
		{
			name:        "complete from current implementation and evidence",
			arrange:     func(*completionProjectionFacts) {},
			wantGoal:    completionpkg.Complete,
			wantProject: completionpkg.Complete,
		},
		{
			name: "planned without accepted implementation",
			arrange: func(facts *completionProjectionFacts) {
				facts.acceptedResources = nil
				facts.verification = &store.VerificationCompletionFacts{
					LatestRuns:      make(map[string]store.LatestValidationResult),
					CurrentEvidence: make(map[string][]store.VerificationEvidence),
				}
				facts.plan.Actions = []planpkg.PlannedAction{{Goals: []string{"goal.ship"}}}
			},
			wantGoal:    completionpkg.Planned,
			wantProject: completionpkg.Planned,
		},
		{
			name: "previously complete goal regresses when evidence is stale",
			arrange: func(facts *completionProjectionFacts) {
				facts.intent.Goals[0].Status = string(completionpkg.Complete)
				facts.intent.CompletionStatus = string(completionpkg.Complete)
				facts.verification.CurrentEvidence = make(map[string][]store.VerificationEvidence)
				facts.verification.LatestRuns["validation.goal"] = store.LatestValidationResult{
					RunID: "run-malformed", Classification: "malformed",
				}
			},
			wantGoal:    completionpkg.Regressed,
			wantProject: completionpkg.Regressed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts := completeProjectionTestFacts()
			tt.arrange(facts)
			index := indexCompletionProjectionFacts(facts)
			projection := deriveCompletionProjection(facts, index)

			require.Len(t, projection.goals, 1)
			require.Equal(t, "goal.ship", projection.goals[0].GoalID)
			require.Equal(t, tt.wantGoal, projection.goals[0].Status)
			require.Equal(t, tt.wantProject, projection.project.Status)
		})
	}
}

func TestCompletionProjectionIndexesTraceabilityRelationships(t *testing.T) {
	facts := completeProjectionTestFacts()
	index := indexCompletionProjectionFacts(facts)

	require.True(t, index.accepted["asset.app"])
	require.True(t, index.goalResources["goal.ship"]["asset.app"])
	require.True(t, index.goalEvidence["goal.ship"]["evidence.acceptance"])
	require.True(t, index.targets["resource\x00asset.app"]["validation.resource"])
	require.True(t, index.targets["capability\x00capability.deliver"]["validation.integration"])
	require.True(t, index.targets["goal\x00goal.ship"]["validation.goal"])
}

func completeProjectionTestFacts() *completionProjectionFacts {
	project := &cuepkg.Project{
		Name: "sample",
		Goals: map[string]cuepkg.Goal{
			"ship": {Capabilities: []string{"capability.deliver"}},
		},
		Capabilities: map[string]cuepkg.Capability{
			"deliver": {
				Goals: []string{"goal.ship"},
				Acceptance: map[string]cuepkg.AcceptanceScenario{
					"works": {Evidence: []string{"evidence.acceptance"}},
				},
			},
		},
		Completion: cuepkg.CompletionPolicy{
			RequiredGoals: []string{"goal.ship"},
			ProjectChecks: []string{"validation.project"},
		},
	}
	definitions := []store.VerificationDefinition{
		{
			DefinitionID: "validation.resource", Scope: "resource",
			Targets: []store.IntentVerificationTarget{{Kind: "resource", ID: "asset.app"}},
		},
		{
			DefinitionID: "validation.integration", Scope: "integration_wave",
			Targets: []store.IntentVerificationTarget{{Kind: "capability", ID: "capability.deliver"}},
		},
		{
			DefinitionID: "validation.goal", Scope: "goal",
			Targets: []store.IntentVerificationTarget{{Kind: "goal", ID: "goal.ship"}},
		},
		{DefinitionID: "validation.project", Scope: "project"},
	}
	latest := make(map[string]store.LatestValidationResult, len(definitions))
	for _, definition := range definitions {
		latest[definition.DefinitionID] = store.LatestValidationResult{
			RunID: definition.DefinitionID + "-run", Classification: "passed",
		}
	}
	return &completionProjectionFacts{
		plan: &PlanResult{
			Registry: &cuepkg.Registry{
				Project: project,
				Resources: map[string]cuepkg.Resource{
					"asset.app": {
						ID:            "asset.app",
						Contributions: []cuepkg.Contribution{{Capability: "capability.deliver"}},
					},
				},
			},
		},
		intent: &store.ProjectIntentState{
			ProjectName: "sample",
			Goals: []store.PersistedGoal{{
				IntentGoal: store.IntentGoal{ID: "goal.ship"}, Status: string(completionpkg.Declared),
			}},
		},
		verification: &store.VerificationCompletionFacts{
			LatestRuns: latest,
			CurrentEvidence: map[string][]store.VerificationEvidence{
				"evidence.acceptance": {{EvidenceID: "evidence.acceptance", Classification: "passed"}},
			},
		},
		definitions:       definitions,
		acceptedResources: []store.Resource{{ID: "asset.app"}},
	}
}
