package spec

import (
	"context"
	"fmt"

	"github.com/crestenstclair/crest-spec/internal/store"
)

type ProjectOverviewResult struct {
	Project        *store.ProjectIntentState           `json:"project"`
	Blockers       []store.CompletionBlocker           `json:"blockers"`
	ProjectHistory []store.StatusTransition            `json:"project_history"`
	GoalHistory    map[string][]store.StatusTransition `json:"goal_history"`
	Contributions  []store.PersistedContribution       `json:"contributions"`
}

// ProjectOverview reconciles current declarations, then reads the canonical
// SQLite projection. Existing operational statuses are preserved by reconcile.
func (s *Spec) ProjectOverview(ctx context.Context) (*ProjectOverviewResult, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan project overview: %w", err)
	}
	project := planResult.Registry.Project
	snapshot, err := projectIntentSnapshot(project)
	if err != nil {
		return nil, fmt.Errorf("project intent: %w", err)
	}
	registry := planResult.Registry
	snapshot.ResourceTrace = resourceTraceSnapshot(registry)
	snapshot.Verification, err = verificationDefinitionSnapshot(registry)
	if err != nil {
		return nil, fmt.Errorf("verification definitions: %w", err)
	}
	if err := s.store.ReconcileProjectIntent(ctx, snapshot); err != nil {
		return nil, fmt.Errorf("reconcile project intent: %w", err)
	}
	if err := s.reprojectCompletion(ctx, planResult, store.TransitionSource{Type: "dashboard_projection", ID: snapshot.SpecHash}); err != nil {
		return nil, fmt.Errorf("project completion: %w", err)
	}
	intent, err := s.store.GetProjectIntent(ctx, project.Name)
	if err != nil {
		return nil, fmt.Errorf("get project intent: %w", err)
	}
	projectHistory, err := s.store.ListProjectStatusHistory(ctx, project.Name)
	if err != nil {
		return nil, err
	}
	blockers, err := s.store.ListCompletionBlockers(ctx, project.Name)
	if err != nil {
		return nil, err
	}
	contributions, err := s.store.ListResourceContributions(ctx, project.Name)
	if err != nil {
		return nil, err
	}
	result := &ProjectOverviewResult{Project: intent, Blockers: blockers, ProjectHistory: projectHistory, GoalHistory: make(map[string][]store.StatusTransition), Contributions: contributions}
	for _, goal := range intent.Goals {
		history, err := s.store.ListGoalStatusHistory(ctx, goal.ID)
		if err != nil {
			return nil, err
		}
		result.GoalHistory[goal.ID] = history
	}
	return result, nil
}
