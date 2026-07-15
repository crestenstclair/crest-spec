package spec

import (
	"context"
	"fmt"
	"sort"
	"strings"

	completionpkg "github.com/crestenstclair/crest-spec/internal/completion"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// reprojectCompletion derives lifecycle state only from accepted resources,
// active plans, and canonical SQLite verification facts. Hosts and generators
// never submit completion status directly.
func (s *Spec) reprojectCompletion(ctx context.Context, planResult *PlanResult, source store.TransitionSource) error {
	if planResult == nil || planResult.Registry == nil || planResult.Registry.Project == nil {
		return fmt.Errorf("project completion: plan registry is required")
	}
	project := planResult.Registry.Project
	intent, err := s.store.GetProjectIntent(ctx, project.Name)
	if err != nil {
		return fmt.Errorf("project completion: intent: %w", err)
	}
	verification, err := s.store.GetVerificationCompletionFacts(ctx, project.Name)
	if err != nil {
		return fmt.Errorf("project completion: verification facts: %w", err)
	}
	definitions, err := s.store.ListVerificationDefinitions(ctx, project.Name)
	if err != nil {
		return fmt.Errorf("project completion: definitions: %w", err)
	}
	acceptedRows, err := s.store.ListResources()
	if err != nil {
		return fmt.Errorf("project completion: resources: %w", err)
	}
	accepted := make(map[string]bool, len(acceptedRows))
	for _, resource := range acceptedRows {
		accepted[resource.ID] = true
	}

	goalStatus := make(map[string]string, len(intent.Goals))
	for _, goal := range intent.Goals {
		goalStatus[goal.ID] = goal.Status
	}
	activePlan := make(map[string]bool)
	for _, action := range planResult.Actions {
		for _, goalID := range action.Goals {
			activePlan[goalID] = true
		}
	}
	capabilityGoals := make(map[string][]string)
	for name, capability := range project.Capabilities {
		capabilityGoals["capability."+name] = append([]string(nil), capability.Goals...)
	}
	goalResources := make(map[string]map[string]bool)
	for resourceID, resource := range planResult.Registry.Resources {
		for _, contribution := range resource.Contributions {
			for _, goalID := range capabilityGoals[contribution.Capability] {
				if goalResources[goalID] == nil {
					goalResources[goalID] = make(map[string]bool)
				}
				goalResources[goalID][resourceID] = true
			}
		}
	}
	goalEvidence := make(map[string]map[string]bool)
	for capabilityName, capability := range project.Capabilities {
		capabilityID := "capability." + capabilityName
		for _, goalID := range capabilityGoals[capabilityID] {
			if goalEvidence[goalID] == nil {
				goalEvidence[goalID] = make(map[string]bool)
			}
			for _, scenario := range capability.Acceptance {
				for _, evidenceID := range scenario.Evidence {
					goalEvidence[goalID][evidenceID] = true
				}
			}
		}
	}

	targets := make(map[string]map[string]bool)
	for _, definition := range definitions {
		for _, target := range definition.Targets {
			key := target.Kind + "\x00" + target.ID
			if targets[key] == nil {
				targets[key] = make(map[string]bool)
			}
			targets[key][definition.DefinitionID] = true
		}
	}
	definitionByID := make(map[string]store.VerificationDefinition, len(definitions))
	for _, definition := range definitions {
		definitionByID[definition.DefinitionID] = definition
	}
	definitionPassed := func(id string) bool {
		return verification.LatestRuns[id].Classification == "passed"
	}
	allDefinitionsPass := func(ids map[string]bool) bool {
		for id := range ids {
			if !definitionPassed(id) {
				return false
			}
		}
		return true
	}
	evidencePasses := func(ids map[string]bool) bool {
		if len(ids) == 0 {
			return false
		}
		for id := range ids {
			records := verification.CurrentEvidence[id]
			if len(records) == 0 || records[0].Classification != "passed" {
				return false
			}
		}
		return true
	}

	projections := make(map[string]completionpkg.GoalProjection, len(project.Goals))
	visiting := make(map[string]bool)
	var deriveGoal func(string) completionpkg.GoalProjection
	deriveGoal = func(goalID string) completionpkg.GoalProjection {
		if projection, exists := projections[goalID]; exists {
			return projection
		}
		if visiting[goalID] {
			return completionpkg.GoalProjection{GoalID: goalID, Status: completionpkg.Blocked, Reason: "goal dependency cycle"}
		}
		visiting[goalID] = true
		name := strings.TrimPrefix(goalID, "goal.")
		goal := project.Goals[name]
		dependenciesComplete := true
		for _, dependencyID := range goal.DependsOn {
			if deriveGoal(dependencyID).Status != completionpkg.Complete {
				dependenciesComplete = false
			}
		}
		resources := goalResources[goalID]
		acceptedCount := 0
		allLocal := len(resources) > 0
		for resourceID := range resources {
			if accepted[resourceID] {
				acceptedCount++
			}
			resourceDefinitions := targets["resource\x00"+resourceID]
			hasLocalPass := false
			for definitionID := range resourceDefinitions {
				definition := definitionByID[definitionID]
				if definition.Scope == "resource" && definitionPassed(definitionID) {
					hasLocalPass = true
				}
			}
			allLocal = allLocal && accepted[resourceID] && hasLocalPass
		}

		goalDefinitions := copyBoolMap(targets["goal\x00"+goalID])
		integrationDefinitions := make(map[string]bool)
		for _, capabilityID := range goal.Capabilities {
			for definitionID := range targets["capability\x00"+capabilityID] {
				goalDefinitions[definitionID] = true
			}
		}
		for definitionID := range goalDefinitions {
			scope := definitionByID[definitionID].Scope
			if scope == "dependency_contract" || scope == "integration_wave" {
				integrationDefinitions[definitionID] = true
			}
		}
		allIntegrated := allLocal && len(integrationDefinitions) > 0 && allDefinitionsPass(integrationDefinitions)
		acceptanceVerified := evidencePasses(goalEvidence[goalID])
		goalCheckDefinitions := make(map[string]bool)
		for definitionID := range goalDefinitions {
			definition := definitionByID[definitionID]
			if definition.Scope == "goal" || definition.Scope == "regression" {
				goalCheckDefinitions[definitionID] = true
			}
		}
		goalChecksPass := allDefinitionsPass(goalCheckDefinitions)
		var blockers []completionpkg.Blocker
		for definitionID := range goalCheckDefinitions {
			latest, exists := verification.LatestRuns[definitionID]
			if !exists || (latest.Classification != "malformed" && latest.Classification != "theater" && latest.Classification != "error") {
				continue
			}
			blockers = append(blockers, completionpkg.Blocker{
				Category: latest.Classification, Reason: "required verification " + definitionID + " is " + latest.Classification,
				SourceType: "validation_run", SourceID: latest.RunID,
			})
		}
		previouslyComplete := goalStatus[goalID] == string(completionpkg.Complete)
		facts := completionpkg.GoalFacts{
			GoalID: goalID, PreviouslyComplete: previouslyComplete,
			ReverificationRequired:  previouslyComplete && (!acceptanceVerified || !goalChecksPass),
			RequiredEvidenceCurrent: acceptanceVerified, RequiredGoalChecksPass: goalChecksPass,
			DependenciesComplete: dependenciesComplete, AcceptanceVerified: acceptanceVerified,
			ContributionCount: len(resources), AcceptedContributions: acceptedCount,
			AllContributionsLocal: allLocal, AllContributionsIntegrated: allIntegrated,
			HasActivePlan: activePlan[goalID], Blockers: blockers,
		}
		projection := completionpkg.DeriveGoal(facts)
		projections[goalID] = projection
		visiting[goalID] = false
		return projection
	}

	goalIDs := make([]string, 0, len(project.Goals))
	for name := range project.Goals {
		goalIDs = append(goalIDs, "goal."+name)
	}
	sort.Strings(goalIDs)
	goalProjections := make([]completionpkg.GoalProjection, 0, len(goalIDs))
	for _, goalID := range goalIDs {
		goalProjections = append(goalProjections, deriveGoal(goalID))
	}
	projectionByID := make(map[string]completionpkg.GoalProjection, len(goalProjections))
	for _, projection := range goalProjections {
		projectionByID[projection.GoalID] = projection
	}
	required := make([]completionpkg.GoalProjection, 0, len(project.Completion.RequiredGoals))
	for _, goalID := range project.Completion.RequiredGoals {
		required = append(required, projectionByID[goalID])
	}
	projectChecks := make(map[string]bool)
	for _, definitionID := range project.Completion.ProjectChecks {
		projectChecks[definitionID] = true
	}
	projectChecksPass := allDefinitionsPass(projectChecks)
	projectPreviouslyComplete := intent.CompletionStatus == string(completionpkg.Complete)
	projectProjection := completionpkg.DeriveProject(completionpkg.ProjectFacts{
		ProjectName: project.Name, PreviouslyComplete: projectPreviouslyComplete,
		ReverificationRequired: projectPreviouslyComplete && !projectChecksPass,
		ProjectChecksPass:      projectChecksPass, RequiredGoals: required,
	})
	return s.store.ApplyCompletionProjection(ctx, project.Name, goalProjections, projectProjection, source)
}

func (s *Spec) refreshCompletion(ctx context.Context, source store.TransitionSource) error {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	return s.reprojectCompletion(ctx, planResult, source)
}

func copyBoolMap(values map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}
