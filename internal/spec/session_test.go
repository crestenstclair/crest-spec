package spec

import (
	"testing"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/graph"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBeginResult_HasInstructions(t *testing.T) {
	result := &BeginResult{
		SessionID:    "sess-1",
		Instructions: "You are a dispatcher",
	}
	assert.Contains(t, result.Instructions, "dispatcher")
}

func TestNextResult_Done(t *testing.T) {
	result := &NextResult{Done: true, WaveIndex: 3}
	assert.True(t, result.Done)
}

func TestContextResult_HasPrompts(t *testing.T) {
	result := &ContextResult{
		SystemPrompt: "You are a go code generator",
		Prompt:       "# Resource: aggregate",
	}
	assert.NotEmpty(t, result.SystemPrompt)
	assert.NotEmpty(t, result.Prompt)
}

// --- forceTargetIntoActions tests ---

func TestForceTargetIntoActions_AddsWhenMissing(t *testing.T) {
	reg := &cuepkg.Registry{
		Resources: map[string]cuepkg.Resource{
			"res.A": {ID: "res.A", Kind: "aggregate"},
			"res.B": {ID: "res.B", Kind: "aggregate"},
		},
	}
	actions := []planpkg.PlannedAction{
		{ResourceID: "res.A", Kind: planpkg.ActionCreate, Reason: "new resource"},
	}

	result := forceTargetIntoActions(actions, "res.B", reg)

	require.Len(t, result, 2)
	assert.Equal(t, "res.B", result[1].ResourceID)
	assert.Equal(t, planpkg.ActionModify, result[1].Kind)
	assert.Equal(t, "forced regeneration", result[1].Reason)
}

func TestForceTargetIntoActions_NoopWhenAlreadyPresent(t *testing.T) {
	reg := &cuepkg.Registry{
		Resources: map[string]cuepkg.Resource{
			"res.A": {ID: "res.A", Kind: "aggregate"},
		},
	}
	actions := []planpkg.PlannedAction{
		{ResourceID: "res.A", Kind: planpkg.ActionModify, Reason: "declaration changed"},
	}

	result := forceTargetIntoActions(actions, "res.A", reg)

	require.Len(t, result, 1)
	assert.Equal(t, "declaration changed", result[0].Reason)
}

func TestForceTargetIntoActions_NoopWhenTargetNotInRegistry(t *testing.T) {
	reg := &cuepkg.Registry{
		Resources: map[string]cuepkg.Resource{
			"res.A": {ID: "res.A", Kind: "aggregate"},
		},
	}
	actions := []planpkg.PlannedAction{
		{ResourceID: "res.A", Kind: planpkg.ActionCreate, Reason: "new resource"},
	}

	result := forceTargetIntoActions(actions, "res.nonexistent", reg)

	require.Len(t, result, 1)
}

func TestForceTargetIntoActions_EmptyActions(t *testing.T) {
	reg := &cuepkg.Registry{
		Resources: map[string]cuepkg.Resource{
			"res.A": {ID: "res.A", Kind: "aggregate"},
		},
	}

	result := forceTargetIntoActions(nil, "res.A", reg)

	require.Len(t, result, 1)
	assert.Equal(t, "res.A", result[0].ResourceID)
	assert.Equal(t, planpkg.ActionModify, result[0].Kind)
	assert.Equal(t, "forced regeneration", result[0].Reason)
}

// --- filterForTarget tests ---

func buildGraph(resources map[string]cuepkg.Resource) *graph.Graph {
	g, _ := graph.Build(resources)
	return g
}

func TestFilterForTarget_KeepsTargetAndAncestors(t *testing.T) {
	// C depends on B, B depends on A. Target = C.
	// Should keep A, B, C and drop D.
	resources := map[string]cuepkg.Resource{
		"res.A": {ID: "res.A", Kind: "aggregate"},
		"res.B": {ID: "res.B", Kind: "aggregate", Dependencies: []cuepkg.Edge{{TargetID: "res.A", Kind: "uses"}}},
		"res.C": {ID: "res.C", Kind: "aggregate", Dependencies: []cuepkg.Edge{{TargetID: "res.B", Kind: "uses"}}},
		"res.D": {ID: "res.D", Kind: "aggregate"},
	}
	g := buildGraph(resources)

	actions := []planpkg.PlannedAction{
		{ResourceID: "res.A", Kind: planpkg.ActionCreate},
		{ResourceID: "res.B", Kind: planpkg.ActionCreate},
		{ResourceID: "res.C", Kind: planpkg.ActionCreate},
		{ResourceID: "res.D", Kind: planpkg.ActionCreate},
	}
	waves := [][]string{
		{"res.A", "res.D"},
		{"res.B"},
		{"res.C"},
	}

	filteredActions, filteredWaves := filterForTarget(actions, waves, "res.C", g)

	// Actions: A, B, C (not D)
	actionIDs := make(map[string]bool)
	for _, a := range filteredActions {
		actionIDs[a.ResourceID] = true
	}
	assert.Len(t, filteredActions, 3)
	assert.True(t, actionIDs["res.A"])
	assert.True(t, actionIDs["res.B"])
	assert.True(t, actionIDs["res.C"])
	assert.False(t, actionIDs["res.D"])

	// Waves: first wave should only have A (D dropped), second B, third C
	require.Len(t, filteredWaves, 3)
	assert.Equal(t, []string{"res.A"}, filteredWaves[0])
	assert.Equal(t, []string{"res.B"}, filteredWaves[1])
	assert.Equal(t, []string{"res.C"}, filteredWaves[2])
}

func TestFilterForTarget_DropsEmptyWaves(t *testing.T) {
	// Target = A, which has no dependencies. Wave 0 has A,
	// Wave 1 has B. After filtering, only wave 0 should remain.
	resources := map[string]cuepkg.Resource{
		"res.A": {ID: "res.A", Kind: "aggregate"},
		"res.B": {ID: "res.B", Kind: "aggregate", Dependencies: []cuepkg.Edge{{TargetID: "res.A", Kind: "uses"}}},
	}
	g := buildGraph(resources)

	actions := []planpkg.PlannedAction{
		{ResourceID: "res.A", Kind: planpkg.ActionCreate},
		{ResourceID: "res.B", Kind: planpkg.ActionCreate},
	}
	waves := [][]string{
		{"res.A"},
		{"res.B"},
	}

	filteredActions, filteredWaves := filterForTarget(actions, waves, "res.A", g)

	assert.Len(t, filteredActions, 1)
	assert.Equal(t, "res.A", filteredActions[0].ResourceID)
	require.Len(t, filteredWaves, 1)
	assert.Equal(t, []string{"res.A"}, filteredWaves[0])
}

func TestFilterForTarget_TargetOnly(t *testing.T) {
	// Target with no dependencies and no dependents.
	resources := map[string]cuepkg.Resource{
		"res.A": {ID: "res.A", Kind: "aggregate"},
		"res.B": {ID: "res.B", Kind: "aggregate"},
	}
	g := buildGraph(resources)

	actions := []planpkg.PlannedAction{
		{ResourceID: "res.A", Kind: planpkg.ActionCreate},
		{ResourceID: "res.B", Kind: planpkg.ActionCreate},
	}
	waves := [][]string{{"res.A", "res.B"}}

	filteredActions, filteredWaves := filterForTarget(actions, waves, "res.A", g)

	assert.Len(t, filteredActions, 1)
	assert.Equal(t, "res.A", filteredActions[0].ResourceID)
	require.Len(t, filteredWaves, 1)
	assert.Equal(t, []string{"res.A"}, filteredWaves[0])
}

func TestFilterForTarget_PreservesTransitiveDeps(t *testing.T) {
	// D -> C -> B -> A. Target = D. All should be kept.
	resources := map[string]cuepkg.Resource{
		"res.A": {ID: "res.A", Kind: "aggregate"},
		"res.B": {ID: "res.B", Kind: "aggregate", Dependencies: []cuepkg.Edge{{TargetID: "res.A", Kind: "uses"}}},
		"res.C": {ID: "res.C", Kind: "aggregate", Dependencies: []cuepkg.Edge{{TargetID: "res.B", Kind: "uses"}}},
		"res.D": {ID: "res.D", Kind: "aggregate", Dependencies: []cuepkg.Edge{{TargetID: "res.C", Kind: "uses"}}},
	}
	g := buildGraph(resources)

	actions := []planpkg.PlannedAction{
		{ResourceID: "res.A", Kind: planpkg.ActionCreate},
		{ResourceID: "res.B", Kind: planpkg.ActionCreate},
		{ResourceID: "res.C", Kind: planpkg.ActionCreate},
		{ResourceID: "res.D", Kind: planpkg.ActionCreate},
	}
	waves := [][]string{{"res.A"}, {"res.B"}, {"res.C"}, {"res.D"}}

	filteredActions, filteredWaves := filterForTarget(actions, waves, "res.D", g)

	assert.Len(t, filteredActions, 4)
	require.Len(t, filteredWaves, 4)
}

func TestFilterForTarget_ActionNotInPlan(t *testing.T) {
	// Target has a dependency that is NOT in the actions list (already settled).
	// The filter should not add it; it only filters existing actions.
	resources := map[string]cuepkg.Resource{
		"res.A": {ID: "res.A", Kind: "aggregate"},
		"res.B": {ID: "res.B", Kind: "aggregate", Dependencies: []cuepkg.Edge{{TargetID: "res.A", Kind: "uses"}}},
	}
	g := buildGraph(resources)

	// Only B has an action; A is already settled.
	actions := []planpkg.PlannedAction{
		{ResourceID: "res.B", Kind: planpkg.ActionModify, Reason: "declaration changed"},
	}
	waves := [][]string{{"res.A"}, {"res.B"}}

	filteredActions, filteredWaves := filterForTarget(actions, waves, "res.B", g)

	// Only B's action is kept (A has no action to filter in).
	assert.Len(t, filteredActions, 1)
	assert.Equal(t, "res.B", filteredActions[0].ResourceID)
	// Wave containing only A is kept because A is in the keep set.
	require.Len(t, filteredWaves, 2)
	assert.Equal(t, []string{"res.A"}, filteredWaves[0])
	assert.Equal(t, []string{"res.B"}, filteredWaves[1])
}

// --- Combined force + target test ---

func TestForceAndFilter_WorkTogether(t *testing.T) {
	// Scenario: B depends on A. Both are settled (no actions in plan).
	// Force + Target on B should:
	// 1. Force adds B to actions
	// 2. Filter keeps only B (and ancestor A, but A has no action)
	resources := map[string]cuepkg.Resource{
		"res.A": {ID: "res.A", Kind: "aggregate"},
		"res.B": {ID: "res.B", Kind: "aggregate", Dependencies: []cuepkg.Edge{{TargetID: "res.A", Kind: "uses"}}},
		"res.C": {ID: "res.C", Kind: "aggregate"},
	}
	reg := &cuepkg.Registry{
		Resources: resources,
	}
	g := buildGraph(resources)

	actions := []planpkg.PlannedAction{
		{ResourceID: "res.C", Kind: planpkg.ActionCreate, Reason: "new resource"},
	}
	waves := [][]string{{"res.A", "res.C"}, {"res.B"}}

	// Step 1: force
	actions = forceTargetIntoActions(actions, "res.B", reg)
	require.Len(t, actions, 2)

	// Step 2: filter
	actions, waves = filterForTarget(actions, waves, "res.B", g)

	// Should only have B (C is filtered out, A has no action)
	require.Len(t, actions, 1)
	assert.Equal(t, "res.B", actions[0].ResourceID)
	assert.Equal(t, "forced regeneration", actions[0].Reason)

	// Waves: wave 0 has A (ancestor, kept in waves), wave 1 has B
	require.Len(t, waves, 2)
	assert.Equal(t, []string{"res.A"}, waves[0])
	assert.Equal(t, []string{"res.B"}, waves[1])
}
