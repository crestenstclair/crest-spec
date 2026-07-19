package cue

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBehavioralContractForResourceIncludesAcceptanceAndExplicitWitness(t *testing.T) {
	project := &Project{
		Capabilities: map[string]Capability{
			"mix": {
				Description: "mix isolated channels",
				Acceptance: map[string]AcceptanceScenario{
					"isolated": {Description: "one channel edit leaves another unchanged"},
				},
			},
		},
		Witnesses: map[string]Witness{
			"isolation": {
				ID: "witness.isolation", Resources: []string{"domainService.Audio.Mixer"},
				Command: []string{"observe", "real"}, NegativeCommand: []string{"observe", "collapsed"},
			},
			"unrelated": {ID: "witness.unrelated", Resources: []string{"adapter.Other"}},
		},
	}
	registry := &Registry{
		Project: project,
		Resources: map[string]Resource{
			"domainService.Audio.Mixer": {ID: "domainService.Audio.Mixer"},
		},
		ResourceCapabilities: map[string][]string{
			"domainService.Audio.Mixer": {"capability.mix"},
		},
	}

	contract := registry.BehavioralContractForResource("domainService.Audio.Mixer")
	require.Contains(t, contract.Capabilities, "capability.mix")
	require.Contains(t, contract.Witnesses, "witness.isolation")
	assert.NotContains(t, contract.Witnesses, "witness.unrelated")
	assert.NotEmpty(t, contract.Hash())

	changed := contract
	witness := changed.Witnesses["witness.isolation"]
	witness.Predicates = []WitnessPredicate{{Field: "isolated", Op: "eq", Value: true}}
	changed.Witnesses = map[string]Witness{"witness.isolation": witness}
	assert.NotEqual(t, contract.Hash(), changed.Hash())
}

func TestVerificationContractIncludesOnlyChecksAndDeclaredRepairWitnesses(t *testing.T) {
	const owner = "domainService.Audio.Mixer"
	registry := &Registry{
		Project: &Project{Witnesses: map[string]Witness{
			"observer": {
				ID: "witness.observer", Resources: []string{owner},
				Command: []string{"observe"}, NegativeCommand: []string{"counterexample"},
			},
			"repair": {
				ID: "witness.repair", Resources: []string{owner}, RepairResources: []string{owner},
				Command: []string{"observe"}, NegativeCommand: []string{"counterexample"},
			},
		}},
		Resources: map[string]Resource{
			owner: {
				ID:          owner,
				Validations: []Validation{{ID: "validation.mixer", Kind: "custom", Command: []string{"check"}}},
			},
		},
	}

	contract := registry.VerificationContractForResource(owner)
	require.Len(t, contract.Validations, 1)
	require.Contains(t, contract.RepairWitnesses, "witness.repair")
	assert.NotContains(t, contract.RepairWitnesses, "witness.observer")
	assert.NotEmpty(t, contract.Hash())

	observer := registry.Project.Witnesses["observer"]
	observer.Timeout = "5s"
	registry.Project.Witnesses["observer"] = observer
	assert.Equal(t, contract.Hash(), registry.VerificationContractForResource(owner).Hash(),
		"editing a broad observer must not schedule its observed resources")

	repair := registry.Project.Witnesses["repair"]
	repair.Timeout = "5s"
	registry.Project.Witnesses["repair"] = repair
	assert.NotEqual(t, contract.Hash(), registry.VerificationContractForResource(owner).Hash())
}

func TestVerificationContractIncludesAmendmentValidation(t *testing.T) {
	check := Validation{ID: "validation.amendment", Kind: "custom", Command: []string{"check-fix"}}
	const resourceID = "asset.Audio.Renderer"
	registry := &Registry{
		Project: &Project{},
		Resources: map[string]Resource{resourceID: {
			ID: resourceID, Kind: "asset",
			Declaration: Asset{Kind: "source", Meta: Meta{Amendments: []Amendment{{
				Name: "repair", Prompt: "repair renderer", Validation: &check,
			}}}},
		}},
	}
	contract := registry.VerificationContractForResource(resourceID)
	require.Len(t, contract.Validations, 1)
	assert.Equal(t, check.ID, contract.Validations[0].ID)
	assert.NotEmpty(t, contract.Hash())
}
