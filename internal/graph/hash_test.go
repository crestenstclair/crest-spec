package graph

import (
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestComputeEffectiveHashes_Stability(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "int"}},
	}
	g, err := Build(resources)
	require.NoError(t, err)
	h1 := ComputeEffectiveHashes(resources, g, "opus", "default")
	h2 := ComputeEffectiveHashes(resources, g, "opus", "default")
	assert.Equal(t, h1["A"], h2["A"])
	assert.NotEmpty(t, h1["A"])
}

func TestComputeEffectiveHashes_DependencyCascade(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "int"},
			Dependencies: []cuepkg.Edge{{TargetID: "B", Kind: "uses"}}},
		"B": {ID: "B", Kind: "aggregate", Declaration: map[string]string{"state": "string"}},
	}
	g, err := Build(resources)
	require.NoError(t, err)
	h1 := ComputeEffectiveHashes(resources, g, "opus", "default")

	resources["B"] = cuepkg.Resource{ID: "B", Kind: "aggregate", Declaration: map[string]string{"state": "bool"}}
	h2 := ComputeEffectiveHashes(resources, g, "opus", "default")
	assert.NotEqual(t, h1["B"], h2["B"])
	assert.NotEqual(t, h1["A"], h2["A"])
}

func TestComputeEffectiveHashes_ModelChange(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "int"}},
	}
	g, err := Build(resources)
	require.NoError(t, err)
	h1 := ComputeEffectiveHashes(resources, g, "opus", "default")
	h2 := ComputeEffectiveHashes(resources, g, "sonnet", "default")
	assert.NotEqual(t, h1["A"], h2["A"])
}

func TestComputeEffectiveHashes_ModeChange(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "int"}},
	}
	g, err := Build(resources)
	require.NoError(t, err)
	h1 := ComputeEffectiveHashes(resources, g, "opus", "default")
	h2 := ComputeEffectiveHashes(resources, g, "opus", "debug")
	assert.NotEqual(t, h1["A"], h2["A"])
}

func TestComputeEffectiveHashes_IndependentUnchanged(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "int"}},
		"B": {ID: "B", Kind: "aggregate", Declaration: map[string]string{"state": "string"}},
	}
	g, err := Build(resources)
	require.NoError(t, err)
	h1 := ComputeEffectiveHashes(resources, g, "opus", "default")

	resources["A"] = cuepkg.Resource{ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "bool"}}
	h2 := ComputeEffectiveHashes(resources, g, "opus", "default")
	assert.NotEqual(t, h1["A"], h2["A"])
	assert.Equal(t, h1["B"], h2["B"])
}

func TestComputeEffectiveHashes_BehavioralContractChange(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"A": {
			ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "int"},
			BehavioralContractHash: "witness-v1",
		},
	}
	g, err := Build(resources)
	require.NoError(t, err)
	h1 := ComputeEffectiveHashes(resources, g, "opus", "default")

	resource := resources["A"]
	resource.BehavioralContractHash = "witness-v2"
	resources["A"] = resource
	h2 := ComputeEffectiveHashes(resources, g, "opus", "default")

	assert.NotEqual(t, h1["A"], h2["A"])
}

func TestGenerationAndVerificationHashesSeparateCodeFromBehavior(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"A": {
			ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "int"},
			BehavioralContractHash: "behavior-v1",
		},
	}
	beforeGeneration := ComputeGenerationInputHashes(resources, nil)
	beforeV2 := ComputeLegacyGenerationInputHashesV2(resources, nil)

	resource := resources["A"]
	resource.BehavioralContractHash = "behavior-v2"
	resources["A"] = resource
	afterGeneration := ComputeGenerationInputHashes(resources, nil)
	afterV2 := ComputeLegacyGenerationInputHashesV2(resources, nil)

	assert.Equal(t, beforeGeneration["A"], afterGeneration["A"],
		"acceptance and witness changes schedule verification, not code generation")
	assert.Equal(t, ComputeVerificationContractHashes(resources)["A"], "",
		"broad behavioral observers are evidence, not repair-owner scheduling")
	assert.NotEqual(t, beforeV2["A"], afterV2["A"],
		"the compatibility hash must exactly preserve the behavior-inclusive v2 algorithm")
	assert.Equal(t, afterGeneration, ComputeGenerationInputHashes(resources, nil),
		"generation-input hashes do not depend on model or mode")
}

func TestGenerationInputHashTracksProjectGeneratorContractForValidation(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "int"}},
		"B": {ID: "B", Kind: "domainService", Declaration: map[string]string{"purpose": "render"}},
	}
	baseProject := func() *cuepkg.Project {
		return &cuepkg.Project{
			Mission: "build a safe synthesizer",
			Meta: cuepkg.Meta{
				Language: "rust",
				Style:    "prefer explicit ownership",
				Rules:    []string{"no callback allocation"},
				Avoid:    []string{"global mutable state"},
			},
			Invariants: cuepkg.FlexInvariants{{
				Text: "audio callbacks are real-time safe",
				Meta: cuepkg.Meta{Rationale: "dropouts are observable failures"},
			}},
		}
	}
	baseline := ComputeGenerationInputHashes(resources, baseProject())
	baselineV2 := ComputeLegacyGenerationInputHashesV2(resources, baseProject())

	mutations := map[string]func(*cuepkg.Project){
		"mission":   func(project *cuepkg.Project) { project.Mission = "build a polyphonic synthesizer" },
		"language":  func(project *cuepkg.Project) { project.Meta.Language = "go" },
		"style":     func(project *cuepkg.Project) { project.Meta.Style = "prefer small interfaces" },
		"rules":     func(project *cuepkg.Project) { project.Meta.Rules = []string{"all callbacks are bounded"} },
		"avoid":     func(project *cuepkg.Project) { project.Meta.Avoid = []string{"blocking locks"} },
		"invariant": func(project *cuepkg.Project) { project.Invariants[0].Text = "all channels remain isolated" },
		"rationale": func(project *cuepkg.Project) { project.Invariants[0].Meta.Rationale = "voices must not cross-talk" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			project := baseProject()
			mutate(project)
			changed := ComputeGenerationInputHashes(resources, project)
			assert.NotEqual(t, baseline["A"], changed["A"])
			assert.NotEqual(t, baseline["B"], changed["B"])
			assert.NotEqual(t, baselineV2, ComputeLegacyGenerationInputHashesV2(resources, project),
				"v2 compatibility must still recognize the historical project-prompt fingerprint")
		})
	}

	unrelated := baseProject()
	unrelated.Name = "renamed without changing the generator prompt"
	assert.Equal(t, baseline, ComputeGenerationInputHashes(resources, unrelated),
		"project metadata not injected by BuildSystemPrompt is not a generation input")
}

func TestGenerationInputHashTracksEffectiveResourceMetaSeparatelyFromCode(t *testing.T) {
	before := map[string]cuepkg.Resource{
		"A": {
			ID: "A", Kind: "asset", Declaration: cuepkg.Asset{Kind: "source", Description: "same"},
			Meta: cuepkg.Meta{Language: "rust", Prompts: []string{"context guidance v1"}},
		},
	}
	after := map[string]cuepkg.Resource{
		"A": {
			ID: "A", Kind: "asset", Declaration: cuepkg.Asset{Kind: "source", Description: "same"},
			Meta: cuepkg.Meta{Language: "rust", Prompts: []string{"context guidance v2"}},
		},
	}
	assert.Equal(t, ComputeCodeDeclarationHashes(before), ComputeCodeDeclarationHashes(after))
	assert.NotEqual(t, ComputeGenerationInputHashes(before, nil), ComputeGenerationInputHashes(after, nil),
		"merged context metadata changes worker input and must move immutable operation identity")
}

func TestValidationDefinitionsAreVerificationOnlyForTypedResources(t *testing.T) {
	oldValidation := cuepkg.Validation{ID: "validation.frequency", Kind: "custom", Command: []string{"old-check"}}
	newValidation := cuepkg.Validation{ID: "validation.frequency", Kind: "custom", Command: []string{"new-check"}}
	oldResources := map[string]cuepkg.Resource{
		"valueObject.Synth.Frequency": {
			ID: "valueObject.Synth.Frequency", Kind: "valueObject",
			Declaration: cuepkg.ValueObject{From: "f64", Validations: []cuepkg.Validation{oldValidation}},
			Validations: []cuepkg.Validation{oldValidation},
		},
		"aggregate.Synth.Voice": {
			ID: "aggregate.Synth.Voice", Kind: "aggregate",
			Dependencies: []cuepkg.Edge{{TargetID: "valueObject.Synth.Frequency", Kind: "uses"}},
		},
	}
	newResources := map[string]cuepkg.Resource{
		"valueObject.Synth.Frequency": {
			ID: "valueObject.Synth.Frequency", Kind: "valueObject",
			Declaration: cuepkg.ValueObject{From: "f64", Validations: []cuepkg.Validation{newValidation}},
			Validations: []cuepkg.Validation{newValidation},
		},
		"aggregate.Synth.Voice": {
			ID: "aggregate.Synth.Voice", Kind: "aggregate",
			Dependencies: []cuepkg.Edge{{TargetID: "valueObject.Synth.Frequency", Kind: "uses"}},
		},
	}

	assert.Equal(t, ComputeCodeDeclarationHashes(oldResources), ComputeCodeDeclarationHashes(newResources))
	assert.Equal(t, ComputeGenerationInputHashes(oldResources, nil), ComputeGenerationInputHashes(newResources, nil))
	assert.Equal(t, ComputeExportedContractHashes(oldResources), ComputeExportedContractHashes(newResources))
	assert.Equal(t,
		ComputeDependencyContractHashes(oldResources, ComputeExportedContractHashes(oldResources)),
		ComputeDependencyContractHashes(newResources, ComputeExportedContractHashes(newResources)),
	)
	assert.NotEqual(t, ComputeLegacyDependencyContractHashesV2(oldResources), ComputeLegacyDependencyContractHashesV2(newResources),
		"the migration proof must reproduce the historical dependency projection that included validations")
	oldVerification := oldResources["valueObject.Synth.Frequency"]
	oldVerification.VerificationContractHash = "validation-v1"
	oldResources[oldVerification.ID] = oldVerification
	newVerification := newResources["valueObject.Synth.Frequency"]
	newVerification.VerificationContractHash = "validation-v2"
	newResources[newVerification.ID] = newVerification
	assert.NotEqual(t, ComputeVerificationContractHashes(oldResources), ComputeVerificationContractHashes(newResources))
}

func TestTraceabilityAndAmendmentValidationDoNotDirtyCodeOrConsumers(t *testing.T) {
	oldCheck := cuepkg.Validation{ID: "validation.old", Kind: "custom", Command: []string{"old-check"}}
	newCheck := cuepkg.Validation{ID: "validation.new", Kind: "custom", Command: []string{"new-check"}}
	before := map[string]cuepkg.Resource{
		"asset.Synth.Control": {
			ID: "asset.Synth.Control", Kind: "asset",
			Declaration: cuepkg.Asset{
				Kind:          "source",
				Meta:          cuepkg.Meta{Amendments: []cuepkg.Amendment{{Name: "fix", Prompt: "same code change", Validation: &oldCheck}}},
				ContributesTo: []cuepkg.Contribution{{Capability: "capability.old", Contribution: "old trace"}},
			},
		},
	}
	after := map[string]cuepkg.Resource{
		"asset.Synth.Control": {
			ID: "asset.Synth.Control", Kind: "asset",
			Declaration: cuepkg.Asset{
				Kind:          "source",
				Meta:          cuepkg.Meta{Amendments: []cuepkg.Amendment{{Name: "fix", Prompt: "same code change", Validation: &newCheck}}},
				ContributesTo: []cuepkg.Contribution{{Capability: "capability.new", Contribution: "new trace"}},
			},
		},
	}

	assert.Equal(t, ComputeCodeDeclarationHashes(before), ComputeCodeDeclarationHashes(after))
	assert.Equal(t, ComputeExportedContractHashes(before), ComputeExportedContractHashes(after))
	beforeRegistry := &cuepkg.Registry{Project: &cuepkg.Project{}, Resources: before}
	afterRegistry := &cuepkg.Registry{Project: &cuepkg.Project{}, Resources: after}
	beforeResource := before["asset.Synth.Control"]
	beforeResource.VerificationContractHash = beforeRegistry.VerificationContractForResource(beforeResource.ID).Hash()
	before[beforeResource.ID] = beforeResource
	afterResource := after["asset.Synth.Control"]
	afterResource.VerificationContractHash = afterRegistry.VerificationContractForResource(afterResource.ID).Hash()
	after[afterResource.ID] = afterResource
	assert.NotEqual(t, ComputeVerificationContractHashes(before), ComputeVerificationContractHashes(after))
}

func TestExportedContractHashIgnoresGuidanceAndBehavioralWitnesses(t *testing.T) {
	base := cuepkg.ValueObject{
		From: "f64", Description: "frequency", Invariants: []string{"finite"},
		Meta: cuepkg.Meta{Prompts: []string{"old guidance"}},
	}
	edited := base
	edited.Description = "frequency in hertz"
	edited.Meta.Prompts = []string{"new guidance"}

	before := ComputeExportedContractHashes(map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "valueObject", Declaration: base, BehavioralContractHash: "behavior-v1"},
	})
	after := ComputeExportedContractHashes(map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "valueObject", Declaration: edited, BehavioralContractHash: "behavior-v2"},
	})

	assert.Equal(t, before["A"], after["A"])
}

func TestExportedContractHashTracksStructuralAPI(t *testing.T) {
	before := ComputeExportedContractHashes(map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "valueObject", Declaration: cuepkg.ValueObject{From: "f64"}},
	})
	after := ComputeExportedContractHashes(map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "valueObject", Declaration: cuepkg.ValueObject{From: "f32"}},
	})

	assert.NotEqual(t, before["A"], after["A"])
}

func TestDependencyContractHashTracksDirectContractsAndEdgeIdentity(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"consumer": {ID: "consumer", Dependencies: []cuepkg.Edge{{TargetID: "provider", Kind: "uses"}}},
		"provider": {ID: "provider"},
	}
	exported := map[string]string{"provider": "contract-v1"}
	before := ComputeDependencyContractHashes(resources, exported)

	exported["provider"] = "contract-v2"
	afterContract := ComputeDependencyContractHashes(resources, exported)
	assert.NotEqual(t, before["consumer"], afterContract["consumer"])

	consumer := resources["consumer"]
	consumer.Dependencies[0].Kind = "owns"
	resources["consumer"] = consumer
	afterKind := ComputeDependencyContractHashes(resources, map[string]string{"provider": "contract-v1"})
	assert.NotEqual(t, before["consumer"], afterKind["consumer"])
	assert.NotEmpty(t, before["provider"], "no dependencies still has a known fingerprint")
}
