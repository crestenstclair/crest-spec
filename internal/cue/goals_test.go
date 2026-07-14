package cue

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func validIntentProject() *Project {
	return &Project{
		Name:    "example",
		Mission: "Let a user complete the example behavior",
		Actors: map[string]Actor{
			"user": {Description: "the example user"},
		},
		Goals: map[string]Goal{
			"complete_example": {
				Description:  "the example works",
				Priority:     "required",
				Actors:       []string{"actor.user"},
				Capabilities: []string{"capability.run_example"},
				Requirements: []string{"requirement.example_output"},
			},
		},
		Capabilities: map[string]Capability{
			"run_example": {
				Description: "run the example",
				Goals:       []string{"goal.complete_example"},
				Acceptance: map[string]AcceptanceScenario{
					"successful_run": {
						Description: "the example produces output",
						Actor:       "actor.user",
						Evidence:    []string{"evidence.example_run"},
					},
				},
			},
		},
		Requirements: map[string]Requirement{
			"example_output": {
				Kind:         "functional",
				Description:  "the example produces output",
				Goals:        []string{"goal.complete_example"},
				Capabilities: []string{"capability.run_example"},
			},
		},
		Evidence: map[string]EvidenceRequirement{
			"example_run": {Kind: "validation", Description: "an executable example run"},
		},
		Completion: CompletionPolicy{RequiredGoals: []string{"goal.complete_example"}},
	}
}

func TestValidateProjectIntentAcceptsValidModel(t *testing.T) {
	require.NoError(t, ValidateProjectIntent(validIntentProject()))
}

func TestValidateProjectIntentReportsAllMissingSections(t *testing.T) {
	err := ValidateProjectIntent(&Project{})
	require.Error(t, err)
	for _, want := range []string{
		"project.name is required",
		"project.mission is required",
		"project.actors must contain at least one entry",
		"project.goals must contain at least one entry",
		"project.capabilities must contain at least one entry",
		"project.requirements must contain at least one entry",
		"project.evidence must contain at least one entry",
		"project.completion.requiredGoals must contain at least one goal reference",
	} {
		require.Contains(t, err.Error(), want)
	}
}

func TestValidateProjectIntentRejectsDanglingAndAsymmetricReferences(t *testing.T) {
	p := validIntentProject()
	goal := p.Goals["complete_example"]
	goal.Capabilities = []string{"capability.missing"}
	p.Goals["complete_example"] = goal

	err := ValidateProjectIntent(p)
	require.Error(t, err)
	require.Contains(t, err.Error(), `references unknown ID "capability.missing"`)
	require.Contains(t, err.Error(), `does not reference "capability.run_example"`)
}

func TestValidateProjectIntentRejectsGoalCyclesDeterministically(t *testing.T) {
	p := validIntentProject()
	p.Goals["prerequisite"] = Goal{
		Description:  "a prerequisite",
		Priority:     "required",
		DependsOn:    []string{"goal.complete_example"},
		Capabilities: []string{"capability.prepare_example"},
	}
	p.Capabilities["prepare_example"] = Capability{
		Description: "prepare the example",
		Goals:       []string{"goal.prerequisite"},
		Acceptance: map[string]AcceptanceScenario{
			"prepared": {Description: "prepared", Evidence: []string{"evidence.example_run"}},
		},
	}
	goal := p.Goals["complete_example"]
	goal.DependsOn = []string{"goal.prerequisite"}
	p.Goals["complete_example"] = goal
	p.Completion.RequiredGoals = append(p.Completion.RequiredGoals, "goal.prerequisite")

	err := ValidateProjectIntent(p)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "goal.complete_example -> goal.prerequisite -> goal.complete_example") ||
		strings.Contains(err.Error(), "goal.prerequisite -> goal.complete_example -> goal.prerequisite"))
}
