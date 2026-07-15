package spec

import (
	"context"
	"fmt"

	impactpkg "github.com/crestenstclair/crest-spec/internal/impact"
)

func (s *Spec) Impact(ctx context.Context, resourceID string) (*impactpkg.Result, error) {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("plan impact: %w", err)
	}
	snapshot, err := projectIntentSnapshot(planResult.Registry.Project)
	if err != nil {
		return nil, err
	}
	snapshot.ResourceTrace = resourceTraceSnapshot(planResult.Registry)
	snapshot.Verification, err = verificationDefinitionSnapshot(planResult.Registry)
	if err != nil {
		return nil, err
	}
	if err := s.store.ReconcileProjectIntent(ctx, snapshot); err != nil {
		return nil, err
	}
	intent, err := s.store.GetProjectIntent(ctx, snapshot.ProjectName)
	if err != nil {
		return nil, err
	}
	completed := make(map[string]bool)
	for _, goal := range intent.Goals {
		completed[goal.ID] = goal.Status == "complete"
	}
	result, err := impactpkg.Analyze(resourceID, planResult.Registry, planResult.Graph, completed)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
