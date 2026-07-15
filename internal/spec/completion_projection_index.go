package spec

import "github.com/crestenstclair/crest-spec/internal/store"

type completionProjectionIndex struct {
	accepted      map[string]bool
	goalStatus    map[string]string
	activePlan    map[string]bool
	goalResources map[string]map[string]bool
	goalEvidence  map[string]map[string]bool
	targets       map[string]map[string]bool
	definitions   map[string]store.VerificationDefinition
}

// indexCompletionProjectionFacts builds lookup-only indexes from immutable
// facts. It is pure and has no access to Spec or persistence.
func indexCompletionProjectionFacts(facts *completionProjectionFacts) completionProjectionIndex {
	index := completionProjectionIndex{
		accepted:      make(map[string]bool, len(facts.acceptedResources)),
		goalStatus:    make(map[string]string, len(facts.intent.Goals)),
		activePlan:    make(map[string]bool),
		goalResources: make(map[string]map[string]bool),
		goalEvidence:  make(map[string]map[string]bool),
		targets:       make(map[string]map[string]bool),
		definitions:   make(map[string]store.VerificationDefinition, len(facts.definitions)),
	}
	for _, resource := range facts.acceptedResources {
		index.accepted[resource.ID] = true
	}
	for _, goal := range facts.intent.Goals {
		index.goalStatus[goal.ID] = goal.Status
	}
	for _, action := range facts.plan.Actions {
		for _, goalID := range action.Goals {
			index.activePlan[goalID] = true
		}
	}

	project := facts.plan.Registry.Project
	capabilityGoals := make(map[string][]string, len(project.Capabilities))
	for name, capability := range project.Capabilities {
		capabilityGoals["capability."+name] = append([]string(nil), capability.Goals...)
	}
	for resourceID, resource := range facts.plan.Registry.Resources {
		for _, contribution := range resource.Contributions {
			for _, goalID := range capabilityGoals[contribution.Capability] {
				addToSetMap(index.goalResources, goalID, resourceID)
			}
		}
	}
	for capabilityName, capability := range project.Capabilities {
		for _, goalID := range capabilityGoals["capability."+capabilityName] {
			for _, scenario := range capability.Acceptance {
				for _, evidenceID := range scenario.Evidence {
					addToSetMap(index.goalEvidence, goalID, evidenceID)
				}
			}
		}
	}
	for _, definition := range facts.definitions {
		index.definitions[definition.DefinitionID] = definition
		for _, target := range definition.Targets {
			addToSetMap(index.targets, target.Kind+"\x00"+target.ID, definition.DefinitionID)
		}
	}
	return index
}

func addToSetMap(index map[string]map[string]bool, key, value string) {
	if index[key] == nil {
		index[key] = make(map[string]bool)
	}
	index[key][value] = true
}
