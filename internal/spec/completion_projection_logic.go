package spec

import (
	"sort"
	"strings"

	completionpkg "github.com/crestenstclair/crest-spec/internal/completion"
)

type completionProjectionResult struct {
	goals   []completionpkg.GoalProjection
	project completionpkg.ProjectProjection
}

// deriveCompletionProjection is pure: it has no persistence access and does
// not mutate the collected canonical facts.
func deriveCompletionProjection(facts *completionProjectionFacts, index completionProjectionIndex) completionProjectionResult {
	deriver := completionProjectionDeriver{
		facts: facts, index: index,
		projections: make(map[string]completionpkg.GoalProjection, len(facts.plan.Registry.Project.Goals)),
		visiting:    make(map[string]bool),
	}
	goals := deriver.deriveGoals()
	return completionProjectionResult{goals: goals, project: deriver.deriveProject(goals)}
}

type completionProjectionDeriver struct {
	facts       *completionProjectionFacts
	index       completionProjectionIndex
	projections map[string]completionpkg.GoalProjection
	visiting    map[string]bool
}

func (d *completionProjectionDeriver) deriveGoals() []completionpkg.GoalProjection {
	goalIDs := make([]string, 0, len(d.facts.plan.Registry.Project.Goals))
	for name := range d.facts.plan.Registry.Project.Goals {
		goalIDs = append(goalIDs, "goal."+name)
	}
	sort.Strings(goalIDs)
	result := make([]completionpkg.GoalProjection, 0, len(goalIDs))
	for _, goalID := range goalIDs {
		result = append(result, d.deriveGoal(goalID))
	}
	return result
}

func (d *completionProjectionDeriver) deriveGoal(goalID string) completionpkg.GoalProjection {
	if projection, exists := d.projections[goalID]; exists {
		return projection
	}
	if d.visiting[goalID] {
		return completionpkg.GoalProjection{GoalID: goalID, Status: completionpkg.Blocked, Reason: "goal dependency cycle"}
	}
	d.visiting[goalID] = true
	goal := d.facts.plan.Registry.Project.Goals[strings.TrimPrefix(goalID, "goal.")]
	dependenciesComplete := true
	for _, dependencyID := range goal.DependsOn {
		if d.deriveGoal(dependencyID).Status != completionpkg.Complete {
			dependenciesComplete = false
		}
	}

	acceptedCount, allLocal := d.localContributionFacts(goalID)
	integrationDefinitions, goalCheckDefinitions := d.goalDefinitionSets(goalID, goal.Capabilities)
	allIntegrated := allLocal && len(integrationDefinitions) > 0 && d.allDefinitionsPass(integrationDefinitions)
	acceptanceVerified := d.evidencePasses(d.index.goalEvidence[goalID])
	goalChecksPass := d.allDefinitionsPass(goalCheckDefinitions)
	blockers := d.goalBlockers(goalCheckDefinitions)
	previouslyComplete := d.index.goalStatus[goalID] == string(completionpkg.Complete)
	resources := d.index.goalResources[goalID]
	projection := completionpkg.DeriveGoal(completionpkg.GoalFacts{
		GoalID: goalID, PreviouslyComplete: previouslyComplete,
		ReverificationRequired:  previouslyComplete && (!acceptanceVerified || !goalChecksPass),
		RequiredEvidenceCurrent: acceptanceVerified, RequiredGoalChecksPass: goalChecksPass,
		DependenciesComplete: dependenciesComplete, AcceptanceVerified: acceptanceVerified,
		ContributionCount: len(resources), AcceptedContributions: acceptedCount,
		AllContributionsLocal: allLocal, AllContributionsIntegrated: allIntegrated,
		HasActivePlan: d.index.activePlan[goalID], Blockers: blockers,
	})
	d.projections[goalID] = projection
	d.visiting[goalID] = false
	return projection
}

func (d *completionProjectionDeriver) localContributionFacts(goalID string) (acceptedCount int, allLocal bool) {
	resources := d.index.goalResources[goalID]
	allLocal = len(resources) > 0
	for resourceID := range resources {
		if d.index.accepted[resourceID] {
			acceptedCount++
		}
		hasLocalPass := false
		for definitionID := range d.index.targets["resource\x00"+resourceID] {
			definition := d.index.definitions[definitionID]
			if definition.Scope == "resource" && d.definitionPassed(definitionID) {
				hasLocalPass = true
			}
		}
		allLocal = allLocal && d.index.accepted[resourceID] && hasLocalPass
	}
	return acceptedCount, allLocal
}

func (d *completionProjectionDeriver) goalDefinitionSets(goalID string, capabilityIDs []string) (integration, checks map[string]bool) {
	all := copyBoolMap(d.index.targets["goal\x00"+goalID])
	for _, capabilityID := range capabilityIDs {
		for definitionID := range d.index.targets["capability\x00"+capabilityID] {
			all[definitionID] = true
		}
	}
	integration = make(map[string]bool)
	checks = make(map[string]bool)
	for definitionID := range all {
		scope := d.index.definitions[definitionID].Scope
		if scope == "dependency_contract" || scope == "integration_wave" {
			integration[definitionID] = true
		}
		if scope == "goal" || scope == "regression" {
			checks[definitionID] = true
		}
	}
	return integration, checks
}

func (d *completionProjectionDeriver) goalBlockers(definitionIDs map[string]bool) []completionpkg.Blocker {
	var blockers []completionpkg.Blocker
	for definitionID := range definitionIDs {
		latest, exists := d.facts.verification.LatestRuns[definitionID]
		if !exists || (latest.Classification != "malformed" && latest.Classification != "theater" && latest.Classification != "error") {
			continue
		}
		blockers = append(blockers, completionpkg.Blocker{
			Category: latest.Classification, Reason: "required verification " + definitionID + " is " + latest.Classification,
			SourceType: "validation_run", SourceID: latest.RunID,
		})
	}
	return blockers
}

func (d *completionProjectionDeriver) deriveProject(goals []completionpkg.GoalProjection) completionpkg.ProjectProjection {
	project := d.facts.plan.Registry.Project
	projectionByID := make(map[string]completionpkg.GoalProjection, len(goals))
	for _, projection := range goals {
		projectionByID[projection.GoalID] = projection
	}
	required := make([]completionpkg.GoalProjection, 0, len(project.Completion.RequiredGoals))
	for _, goalID := range project.Completion.RequiredGoals {
		required = append(required, projectionByID[goalID])
	}
	projectChecks := make(map[string]bool, len(project.Completion.ProjectChecks))
	for _, definitionID := range project.Completion.ProjectChecks {
		projectChecks[definitionID] = true
	}
	projectChecksPass := d.allDefinitionsPass(projectChecks)
	previouslyComplete := d.facts.intent.CompletionStatus == string(completionpkg.Complete)
	return completionpkg.DeriveProject(completionpkg.ProjectFacts{
		ProjectName: project.Name, PreviouslyComplete: previouslyComplete,
		ReverificationRequired: previouslyComplete && !projectChecksPass,
		ProjectChecksPass:      projectChecksPass, RequiredGoals: required,
	})
}

func (d *completionProjectionDeriver) definitionPassed(id string) bool {
	return d.facts.verification.LatestRuns[id].Classification == "passed"
}

func (d *completionProjectionDeriver) allDefinitionsPass(ids map[string]bool) bool {
	for id := range ids {
		if !d.definitionPassed(id) {
			return false
		}
	}
	return true
}

func (d *completionProjectionDeriver) evidencePasses(ids map[string]bool) bool {
	if len(ids) == 0 {
		return false
	}
	for id := range ids {
		records := d.facts.verification.CurrentEvidence[id]
		if len(records) == 0 || records[0].Classification != "passed" {
			return false
		}
	}
	return true
}

func copyBoolMap(values map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}
