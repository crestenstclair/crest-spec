// Package impact traces a concrete resource change through structural
// dependents into capabilities, goals, and acceptance evidence.
package impact

import (
	"fmt"
	"sort"
	"strings"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	graphpkg "github.com/crestenstclair/crest-spec/internal/graph"
)

type Result struct {
	SourceResource    string   `json:"source_resource"`
	AffectedResources []string `json:"affected_resources"`
	Capabilities      []string `json:"capabilities"`
	Goals             []string `json:"goals"`
	Evidence          []string `json:"evidence"`
	RegressedGoals    []string `json:"regressed_goals,omitempty"`
	Explanation       []string `json:"explanation"`
}

// Analyze is deterministic and deliberately conservative across structural
// dependency edges, while avoiding unrelated goals that merely share a plan.
func Analyze(sourceResource string, registry *cuepkg.Registry, graph *graphpkg.Graph, completedGoals map[string]bool) (Result, error) {
	if !graph.Has(sourceResource) {
		return Result{}, fmt.Errorf("impact source resource not found: %s", sourceResource)
	}
	result := Result{SourceResource: sourceResource}
	result.AffectedResources = append([]string{sourceResource}, graph.Dependents(sourceResource)...)
	sort.Strings(result.AffectedResources)

	capabilities := make(map[string]bool)
	for _, resourceID := range result.AffectedResources {
		for _, capabilityID := range registry.CapabilitiesForResource(resourceID) {
			capabilities[capabilityID] = true
			result.Explanation = append(result.Explanation, resourceID+" contributes to "+capabilityID)
		}
	}
	for capabilityID := range capabilities {
		result.Capabilities = append(result.Capabilities, capabilityID)
		capability := registry.Project.Capabilities[strings.TrimPrefix(capabilityID, "capability.")]
		result.Goals = append(result.Goals, capability.Goals...)
		for _, scenario := range capability.Acceptance {
			result.Evidence = append(result.Evidence, scenario.Evidence...)
		}
	}
	result.Capabilities = sortedUnique(result.Capabilities)
	result.Goals = sortedUnique(result.Goals)
	result.Evidence = sortedUnique(result.Evidence)
	result.Explanation = sortedUnique(result.Explanation)
	for _, goalID := range result.Goals {
		if completedGoals[goalID] {
			result.RegressedGoals = append(result.RegressedGoals, goalID)
		}
	}
	return result, nil
}

func sortedUnique(values []string) []string {
	sort.Strings(values)
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
