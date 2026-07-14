package impact

import (
	"testing"

	"github.com/stretchr/testify/require"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	graphpkg "github.com/crestenstclair/crest-spec/internal/graph"
)

func TestAnalyzeTracesDependentsButNotUnrelatedGoals(t *testing.T) {
	project := &cuepkg.Project{
		Goals: map[string]cuepkg.Goal{
			"play": {Capabilities: []string{"capability.render"}},
			"docs": {Capabilities: []string{"capability.document"}},
		},
		Capabilities: map[string]cuepkg.Capability{
			"render":   {Goals: []string{"goal.play"}, Acceptance: map[string]cuepkg.AcceptanceScenario{"tone": {Evidence: []string{"evidence.tone"}}}},
			"document": {Goals: []string{"goal.docs"}, Acceptance: map[string]cuepkg.AcceptanceScenario{"guide": {Evidence: []string{"evidence.guide"}}}},
		},
		Contexts: map[string]cuepkg.Context{"Engine": {
			Purpose:        "audio",
			Aggregates:     map[string]cuepkg.Aggregate{"Voice": {ContributesTo: []cuepkg.Contribution{{Capability: "capability.render", Contribution: "voice"}}}},
			DomainServices: map[string]cuepkg.DomainService{"Renderer": {Uses: []string{"aggregate.Engine.Voice"}, ContributesTo: []cuepkg.Contribution{{Capability: "capability.render", Contribution: "render"}}}},
		}},
		AssetKinds: map[string]cuepkg.AssetKind{"doc": {Description: "docs"}},
		Assets:     map[string]cuepkg.Asset{"Guide": {Kind: "doc", ContributesTo: []cuepkg.Contribution{{Capability: "capability.document", Contribution: "guide"}}}},
	}
	registry, err := cuepkg.NewRegistry(project)
	require.NoError(t, err)
	graph, err := graphpkg.Build(registry.Resources)
	require.NoError(t, err)

	result, err := Analyze("aggregate.Engine.Voice", registry, graph, map[string]bool{"goal.play": true, "goal.docs": true})
	require.NoError(t, err)
	require.Equal(t, []string{"aggregate.Engine.Voice", "domainService.Engine.Renderer"}, result.AffectedResources)
	require.Equal(t, []string{"capability.render"}, result.Capabilities)
	require.Equal(t, []string{"goal.play"}, result.Goals)
	require.Equal(t, []string{"evidence.tone"}, result.Evidence)
	require.Equal(t, []string{"goal.play"}, result.RegressedGoals)
}
