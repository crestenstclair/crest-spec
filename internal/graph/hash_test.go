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
	h1 := ComputeEffectiveHashes(resources, g, "opus")
	h2 := ComputeEffectiveHashes(resources, g, "opus")
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
	h1 := ComputeEffectiveHashes(resources, g, "opus")

	resources["B"] = cuepkg.Resource{ID: "B", Kind: "aggregate", Declaration: map[string]string{"state": "bool"}}
	h2 := ComputeEffectiveHashes(resources, g, "opus")
	assert.NotEqual(t, h1["B"], h2["B"])
	assert.NotEqual(t, h1["A"], h2["A"])
}

func TestComputeEffectiveHashes_ModelChange(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "int"}},
	}
	g, err := Build(resources)
	require.NoError(t, err)
	h1 := ComputeEffectiveHashes(resources, g, "opus")
	h2 := ComputeEffectiveHashes(resources, g, "sonnet")
	assert.NotEqual(t, h1["A"], h2["A"])
}

func TestComputeEffectiveHashes_IndependentUnchanged(t *testing.T) {
	resources := map[string]cuepkg.Resource{
		"A": {ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "int"}},
		"B": {ID: "B", Kind: "aggregate", Declaration: map[string]string{"state": "string"}},
	}
	g, err := Build(resources)
	require.NoError(t, err)
	h1 := ComputeEffectiveHashes(resources, g, "opus")

	resources["A"] = cuepkg.Resource{ID: "A", Kind: "aggregate", Declaration: map[string]string{"state": "bool"}}
	h2 := ComputeEffectiveHashes(resources, g, "opus")
	assert.NotEqual(t, h1["A"], h2["A"])
	assert.Equal(t, h1["B"], h2["B"])
}
