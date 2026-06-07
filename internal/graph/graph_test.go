package graph

import (
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeResources(edges map[string][]cuepkg.Edge) map[string]cuepkg.Resource {
	resources := make(map[string]cuepkg.Resource)
	for id, deps := range edges {
		resources[id] = cuepkg.Resource{
			ID:           id,
			Kind:         "aggregate",
			Dependencies: deps,
		}
	}
	return resources
}

func TestBuild_Simple(t *testing.T) {
	resources := makeResources(map[string][]cuepkg.Edge{
		"A": {{TargetID: "B", Kind: "uses"}},
		"B": nil,
		"C": {{TargetID: "B", Kind: "uses"}},
	})
	g, err := Build(resources)
	require.NoError(t, err)
	assert.True(t, g.Has("A"))
	assert.True(t, g.Has("B"))
	assert.True(t, g.Has("C"))
	assert.False(t, g.Has("D"))
}

func TestTopologicalSort_Linear(t *testing.T) {
	resources := makeResources(map[string][]cuepkg.Edge{
		"A": {{TargetID: "B", Kind: "uses"}},
		"B": {{TargetID: "C", Kind: "uses"}},
		"C": nil,
	})
	g, err := Build(resources)
	require.NoError(t, err)
	order, err := g.TopologicalSort()
	require.NoError(t, err)
	idxC := indexOf(order, "C")
	idxB := indexOf(order, "B")
	idxA := indexOf(order, "A")
	assert.Less(t, idxC, idxB)
	assert.Less(t, idxB, idxA)
}

func TestTopologicalSort_Cycle(t *testing.T) {
	resources := makeResources(map[string][]cuepkg.Edge{
		"A": {{TargetID: "B", Kind: "uses"}},
		"B": {{TargetID: "A", Kind: "uses"}},
	})
	g, err := Build(resources)
	require.NoError(t, err)
	_, err = g.TopologicalSort()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestTopologicalSort_Empty(t *testing.T) {
	g, err := Build(map[string]cuepkg.Resource{})
	require.NoError(t, err)
	order, err := g.TopologicalSort()
	require.NoError(t, err)
	assert.Empty(t, order)
}

func TestWaves_Diamond(t *testing.T) {
	//     A
	//    / \
	//   B   C
	//    \ /
	//     D
	resources := makeResources(map[string][]cuepkg.Edge{
		"A": {{TargetID: "B", Kind: "uses"}, {TargetID: "C", Kind: "uses"}},
		"B": {{TargetID: "D", Kind: "uses"}},
		"C": {{TargetID: "D", Kind: "uses"}},
		"D": nil,
	})
	g, err := Build(resources)
	require.NoError(t, err)
	waves, err := g.Waves()
	require.NoError(t, err)
	require.Len(t, waves, 3)
	assert.ElementsMatch(t, []string{"D"}, waves[0])
	assert.ElementsMatch(t, []string{"B", "C"}, waves[1])
	assert.ElementsMatch(t, []string{"A"}, waves[2])
}

func TestWaves_NoDeps(t *testing.T) {
	resources := makeResources(map[string][]cuepkg.Edge{
		"A": nil,
		"B": nil,
		"C": nil,
	})
	g, err := Build(resources)
	require.NoError(t, err)
	waves, err := g.Waves()
	require.NoError(t, err)
	require.Len(t, waves, 1)
	assert.ElementsMatch(t, []string{"A", "B", "C"}, waves[0])
}

func TestAncestors(t *testing.T) {
	resources := makeResources(map[string][]cuepkg.Edge{
		"A": {{TargetID: "B", Kind: "uses"}},
		"B": {{TargetID: "C", Kind: "uses"}},
		"C": nil,
	})
	g, err := Build(resources)
	require.NoError(t, err)
	ancestors := g.Ancestors("A")
	assert.ElementsMatch(t, []string{"B", "C"}, ancestors)
	ancestors = g.Ancestors("C")
	assert.Empty(t, ancestors)
}

func TestDependents(t *testing.T) {
	resources := makeResources(map[string][]cuepkg.Edge{
		"A": {{TargetID: "B", Kind: "uses"}},
		"B": {{TargetID: "C", Kind: "uses"}},
		"C": nil,
	})
	g, err := Build(resources)
	require.NoError(t, err)
	deps := g.Dependents("C")
	assert.ElementsMatch(t, []string{"B", "A"}, deps)
	deps = g.Dependents("A")
	assert.Empty(t, deps)
}

func indexOf(slice []string, s string) int {
	for i, v := range slice {
		if v == s {
			return i
		}
	}
	return -1
}
