package graph

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

// The hash split: guidance (prompts, descriptions, notes, rationale, style…)
// must not feed the effective hash. A prompt tweak regenerates the edited
// resource (declaration hash, which stays over the FULL declaration) but must
// not cascade a regeneration through every dependent.

func twoResourceRegistry(vo cuepkg.ValueObject) (map[string]cuepkg.Resource, *Graph) {
	resources := map[string]cuepkg.Resource{
		"valueObject.Kernel.Frequency": {
			ID: "valueObject.Kernel.Frequency", Kind: "valueObject",
			Declaration: vo,
		},
		"aggregate.Synth.Voice": {
			ID: "aggregate.Synth.Voice", Kind: "aggregate",
			Declaration:  cuepkg.Aggregate{State: map[string]string{"freq": "Frequency"}},
			Dependencies: []cuepkg.Edge{{TargetID: "valueObject.Kernel.Frequency", Kind: "uses"}},
		},
	}
	g, err := Build(resources)
	if err != nil {
		panic(err)
	}
	return resources, g
}

func TestGuidanceOnlyChangeDoesNotMoveEffectiveHash(t *testing.T) {
	base := cuepkg.ValueObject{
		From:        "f64",
		Description: "frequency in Hz",
		Invariants:  []string{"must be positive"},
		Meta:        cuepkg.Meta{Prompts: []string{"original prompt"}},
	}
	edited := base
	edited.Description = "frequency in Hz (audible band)"
	edited.Meta = cuepkg.Meta{Prompts: []string{"a completely different, much longer prompt"}, Notes: "war story"}

	resA, gA := twoResourceRegistry(base)
	resB, gB := twoResourceRegistry(edited)

	hashesA := ComputeEffectiveHashes(resA, gA, "sonnet", "default")
	hashesB := ComputeEffectiveHashes(resB, gB, "sonnet", "default")

	assert.Equal(t, hashesA["valueObject.Kernel.Frequency"], hashesB["valueObject.Kernel.Frequency"],
		"guidance-only edits must not move the resource's effective hash")
	assert.Equal(t, hashesA["aggregate.Synth.Voice"], hashesB["aggregate.Synth.Voice"],
		"guidance-only edits must not cascade to dependents")
}

func TestStructuralChangeMovesEffectiveHashAndCascades(t *testing.T) {
	base := cuepkg.ValueObject{From: "f64", Invariants: []string{"must be positive"}}

	casesEdit := map[string]cuepkg.ValueObject{
		"type change":      {From: "f32", Invariants: []string{"must be positive"}},
		"invariant change": {From: "f64", Invariants: []string{"must be positive", "must be finite"}},
		"state change":     {From: "f64", Invariants: []string{"must be positive"}, State: map[string]string{"hz": "f64"}},
	}

	resA, gA := twoResourceRegistry(base)
	hashesA := ComputeEffectiveHashes(resA, gA, "sonnet", "default")

	for name, edited := range casesEdit {
		resB, gB := twoResourceRegistry(edited)
		hashesB := ComputeEffectiveHashes(resB, gB, "sonnet", "default")
		assert.NotEqual(t, hashesA["valueObject.Kernel.Frequency"], hashesB["valueObject.Kernel.Frequency"],
			"%s must move the effective hash", name)
		assert.NotEqual(t, hashesA["aggregate.Synth.Voice"], hashesB["aggregate.Synth.Voice"],
			"%s must cascade to dependents", name)
	}
}

func TestStructuralJSONStripsGuidanceRecursively(t *testing.T) {
	decl := cuepkg.Aggregate{
		Purpose:    "polyphonic voice",
		State:      map[string]string{"freq": "Frequency"},
		Invariants: []string{"voice reclaimable only when Idle"},
		Meta: cuepkg.Meta{
			Prompts:   []string{"implementation lore"},
			Rationale: "why",
			Style:     "idiomatic",
		},
	}
	out := string(StructuralJSON(decl))
	require.NotEmpty(t, out)
	assert.NotContains(t, out, "implementation lore")
	assert.NotContains(t, out, "polyphonic voice", "purpose is guidance")
	assert.Contains(t, out, "Frequency", "state is structural")
	assert.Contains(t, out, "voice reclaimable only when Idle", "invariants are structural")
}
