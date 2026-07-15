package cue

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateProjectIntent validates the strict goal-oriented project contract.
// It returns all discoverable errors in deterministic order so authors can
// repair one CUE edit rather than encountering failures one at a time.
func ValidateProjectIntent(p *Project) error {
	if p == nil {
		return fmt.Errorf("project intent: project is required")
	}

	var errs []string
	requireText(&errs, "project.name", p.Name)
	requireText(&errs, "project.mission", p.Mission)
	requireMap(&errs, "project.actors", len(p.Actors))
	requireMap(&errs, "project.goals", len(p.Goals))
	requireMap(&errs, "project.capabilities", len(p.Capabilities))
	requireMap(&errs, "project.requirements", len(p.Requirements))
	requireMap(&errs, "project.evidence", len(p.Evidence))
	if len(p.Completion.RequiredGoals) == 0 {
		errs = append(errs, "project.completion.requiredGoals must contain at least one goal reference")
	}

	actorIDs := prefixedIDs("actor", p.Actors)
	goalIDs := prefixedIDs("goal", p.Goals)
	capabilityIDs := prefixedIDs("capability", p.Capabilities)
	requirementIDs := prefixedIDs("requirement", p.Requirements)
	evidenceIDs := prefixedIDs("evidence", p.Evidence)

	for _, name := range sortedMapKeys(p.Actors) {
		requireText(&errs, "project.actors."+name+".description", p.Actors[name].Description)
	}

	for _, name := range sortedMapKeys(p.Goals) {
		goal := p.Goals[name]
		path := "project.goals." + name
		requireText(&errs, path+".description", goal.Description)
		if goal.Priority != "required" && goal.Priority != "optional" {
			errs = append(errs, path+`.priority must be "required" or "optional"`)
		}
		if len(goal.Capabilities) == 0 {
			errs = append(errs, path+".capabilities must contain at least one capability reference")
		}
		validateRefs(&errs, path+".actors", goal.Actors, actorIDs)
		validateRefs(&errs, path+".dependsOn", goal.DependsOn, goalIDs)
		validateRefs(&errs, path+".capabilities", goal.Capabilities, capabilityIDs)
		validateRefs(&errs, path+".requirements", goal.Requirements, requirementIDs)
		self := "goal." + name
		for _, dep := range goal.DependsOn {
			if dep == self {
				errs = append(errs, path+".dependsOn must not reference itself")
			}
		}
	}

	for _, name := range sortedMapKeys(p.Capabilities) {
		capability := p.Capabilities[name]
		path := "project.capabilities." + name
		requireText(&errs, path+".description", capability.Description)
		if len(capability.Goals) == 0 {
			errs = append(errs, path+".goals must contain at least one goal reference")
		}
		if len(capability.Acceptance) == 0 {
			errs = append(errs, path+".acceptance must contain at least one scenario")
		}
		validateRefs(&errs, path+".goals", capability.Goals, goalIDs)
		for _, scenarioName := range sortedMapKeys(capability.Acceptance) {
			scenario := capability.Acceptance[scenarioName]
			scenarioPath := path + ".acceptance." + scenarioName
			requireText(&errs, scenarioPath+".description", scenario.Description)
			if scenario.Actor != "" {
				validateRefs(&errs, scenarioPath+".actor", []string{scenario.Actor}, actorIDs)
			}
			if len(scenario.Evidence) == 0 {
				errs = append(errs, scenarioPath+".evidence must contain at least one evidence reference")
			}
			validateRefs(&errs, scenarioPath+".evidence", scenario.Evidence, evidenceIDs)
			for i, step := range scenario.Steps {
				requireText(&errs, fmt.Sprintf("%s.steps[%d].action", scenarioPath, i), step.Action)
				requireText(&errs, fmt.Sprintf("%s.steps[%d].observes", scenarioPath, i), step.Observes)
			}
		}
	}

	for _, name := range sortedMapKeys(p.Requirements) {
		requirement := p.Requirements[name]
		path := "project.requirements." + name
		if requirement.Kind != "functional" && requirement.Kind != "nonfunctional" {
			errs = append(errs, path+`.kind must be "functional" or "nonfunctional"`)
		}
		requireText(&errs, path+".description", requirement.Description)
		validateRefs(&errs, path+".goals", requirement.Goals, goalIDs)
		validateRefs(&errs, path+".capabilities", requirement.Capabilities, capabilityIDs)
		if len(requirement.Goals) == 0 && len(requirement.Capabilities) == 0 {
			errs = append(errs, path+" must reference at least one goal or capability")
		}
	}

	for _, name := range sortedMapKeys(p.Evidence) {
		evidence := p.Evidence[name]
		path := "project.evidence." + name
		requireText(&errs, path+".kind", evidence.Kind)
		requireText(&errs, path+".description", evidence.Description)
	}

	validateRefs(&errs, "project.completion.requiredGoals", p.Completion.RequiredGoals, goalIDs)
	normalizeAndValidateVerificationDefinitions(p, &errs)
	validateGoalCapabilitySymmetry(&errs, p)
	validateGoalCycles(&errs, p.Goals)

	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return fmt.Errorf("invalid project intent:\n  %s", strings.Join(errs, "\n  "))
}

func requireText(errs *[]string, path, value string) {
	if strings.TrimSpace(value) == "" {
		*errs = append(*errs, path+" is required")
	}
}

func requireMap(errs *[]string, path string, count int) {
	if count == 0 {
		*errs = append(*errs, path+" must contain at least one entry")
	}
}

func validateRefs(errs *[]string, path string, refs []string, valid map[string]bool) {
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if seen[ref] {
			*errs = append(*errs, fmt.Sprintf("%s contains duplicate reference %q", path, ref))
			continue
		}
		seen[ref] = true
		if !valid[ref] {
			*errs = append(*errs, fmt.Sprintf("%s references unknown ID %q", path, ref))
		}
	}
}

func validateGoalCapabilitySymmetry(errs *[]string, p *Project) {
	for goalName, goal := range p.Goals {
		goalID := "goal." + goalName
		for _, capabilityID := range goal.Capabilities {
			capabilityName := strings.TrimPrefix(capabilityID, "capability.")
			capability, ok := p.Capabilities[capabilityName]
			if ok && !contains(capability.Goals, goalID) {
				*errs = append(*errs, fmt.Sprintf("project.goals.%s.capabilities references %q but the capability does not reference %q", goalName, capabilityID, goalID))
			}
		}
	}
	for capabilityName, capability := range p.Capabilities {
		capabilityID := "capability." + capabilityName
		for _, goalID := range capability.Goals {
			goalName := strings.TrimPrefix(goalID, "goal.")
			goal, ok := p.Goals[goalName]
			if ok && !contains(goal.Capabilities, capabilityID) {
				*errs = append(*errs, fmt.Sprintf("project.capabilities.%s.goals references %q but the goal does not reference %q", capabilityName, goalID, capabilityID))
			}
		}
	}
}

func validateGoalCycles(errs *[]string, goals map[string]Goal) {
	state := make(map[string]uint8, len(goals))
	stack := make([]string, 0, len(goals))
	var visit func(string)
	visit = func(id string) {
		if state[id] == 2 {
			return
		}
		if state[id] == 1 {
			start := 0
			for i, v := range stack {
				if v == id {
					start = i
					break
				}
			}
			cycle := append(append([]string{}, stack[start:]...), id)
			*errs = append(*errs, "project goal dependency cycle: "+strings.Join(cycle, " -> "))
			return
		}
		state[id] = 1
		stack = append(stack, id)
		goalName := strings.TrimPrefix(id, "goal.")
		for _, dep := range goals[goalName].DependsOn {
			if _, ok := goals[strings.TrimPrefix(dep, "goal.")]; ok {
				visit(dep)
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = 2
	}
	for _, name := range sortedMapKeys(goals) {
		visit("goal." + name)
	}
}

func prefixedIDs[T any](prefix string, values map[string]T) map[string]bool {
	ids := make(map[string]bool, len(values))
	for key := range values {
		ids[prefix+"."+key] = true
	}
	return ids
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
