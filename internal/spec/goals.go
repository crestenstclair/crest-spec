package spec

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// projectIntentSnapshot maps the strict CUE intent layer into stable SQLite
// identities. DDD and asset declarations remain separate architectural
// resources; this projection contains only project intent and acceptance.
func projectIntentSnapshot(project *cuepkg.Project) (store.ProjectIntentSnapshot, error) {
	if err := cuepkg.ValidateProjectIntent(project); err != nil {
		return store.ProjectIntentSnapshot{}, err
	}

	snapshot := store.ProjectIntentSnapshot{
		ProjectName:   project.Name,
		Mission:       project.Mission,
		RequiredGoals: append([]string(nil), project.Completion.RequiredGoals...),
	}

	for _, name := range sortedIntentKeys(project.Actors) {
		actor := project.Actors[name]
		snapshot.Actors = append(snapshot.Actors, store.IntentActor{ID: "actor." + name, Description: actor.Description})
	}
	for _, name := range sortedIntentKeys(project.Goals) {
		goal := project.Goals[name]
		snapshot.Goals = append(snapshot.Goals, store.IntentGoal{
			ID: "goal." + name, Description: goal.Description, Priority: goal.Priority,
			Actors: append([]string(nil), goal.Actors...), DependsOn: append([]string(nil), goal.DependsOn...),
		})
	}
	for _, name := range sortedIntentKeys(project.Capabilities) {
		capability := project.Capabilities[name]
		capabilityID := "capability." + name
		snapshot.Capabilities = append(snapshot.Capabilities, store.IntentCapability{
			ID: capabilityID, Description: capability.Description, Goals: append([]string(nil), capability.Goals...),
		})
		for ordinal, scenarioName := range sortedIntentKeys(capability.Acceptance) {
			scenario := capability.Acceptance[scenarioName]
			acceptance := store.IntentAcceptance{
				ID: "acceptance." + name + "." + scenarioName, CapabilityID: capabilityID,
				ActorID: scenario.Actor, Description: scenario.Description, Ordinal: ordinal,
				Evidence: append([]string(nil), scenario.Evidence...),
			}
			for _, step := range scenario.Steps {
				acceptance.Steps = append(acceptance.Steps, store.IntentAcceptanceStep{Action: step.Action, Observes: step.Observes})
			}
			snapshot.Acceptance = append(snapshot.Acceptance, acceptance)
		}
	}
	for _, name := range sortedIntentKeys(project.Requirements) {
		requirement := project.Requirements[name]
		snapshot.Requirements = append(snapshot.Requirements, store.IntentRequirement{
			ID: "requirement." + name, Kind: requirement.Kind, Description: requirement.Description,
			Goals: append([]string(nil), requirement.Goals...), Capabilities: append([]string(nil), requirement.Capabilities...),
		})
	}
	for _, name := range sortedIntentKeys(project.Evidence) {
		evidence := project.Evidence[name]
		snapshot.Evidence = append(snapshot.Evidence, store.IntentEvidence{ID: "evidence." + name, Kind: evidence.Kind, Description: evidence.Description})
	}
	for _, name := range sortedIntentKeys(project.NonGoals) {
		snapshot.NonGoals = append(snapshot.NonGoals, store.IntentNonGoal{ID: "non_goal." + name, Description: project.NonGoals[name]})
	}

	canonical, err := json.Marshal(snapshot)
	if err != nil {
		return store.ProjectIntentSnapshot{}, fmt.Errorf("marshal project intent: %w", err)
	}
	snapshot.SpecHash = fmt.Sprintf("%x", sha256.Sum256(canonical))
	return snapshot, nil
}

func sortedIntentKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
