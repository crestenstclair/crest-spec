package plan

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testdataDir(sub string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "cue", "testdata", sub)
}

func TestIntegration_FullPipeline(t *testing.T) {
	// Step 1: Load CUE
	project, err := cuepkg.Load(testdataDir("minimal"))
	require.NoError(t, err)

	// Step 2: Build registry
	reg, err := cuepkg.NewRegistry(project)
	require.NoError(t, err)
	assert.NotEmpty(t, reg.Resources)

	// Step 3: Build graph
	g, err := graph.Build(reg.Resources)
	require.NoError(t, err)

	// Step 4: Verify topo sort works (no cycles)
	order, err := g.TopologicalSort()
	require.NoError(t, err)
	assert.NotEmpty(t, order)

	// Step 5: Verify waves work
	waves, err := g.Waves()
	require.NoError(t, err)
	assert.NotEmpty(t, waves)

	// Step 6: Compute hashes
	hashes := graph.ComputeEffectiveHashes(reg.Resources, g, "opus", "default")
	assert.NotEmpty(t, hashes)

	// Step 7: Plan against empty store → all creates
	st := newFakeStore()
	fs := newFakeFS()
	p := New(st, fs)

	actions, err := p.Plan(context.Background(), reg, g, "opus", "default")
	require.NoError(t, err)

	// Should only have actions for non-structural resources
	for _, a := range actions {
		assert.Equal(t, ActionCreate, a.Kind)
		r := reg.Resources[a.ResourceID]
		assert.False(t, structuralKinds[r.Kind], "structural kind %s should not have an action", r.Kind)
	}

	// Count expected non-structural resources
	expectedCount := 0
	for _, r := range reg.Resources {
		if !structuralKinds[r.Kind] {
			expectedCount++
		}
	}
	assert.Equal(t, expectedCount, len(actions))
}
