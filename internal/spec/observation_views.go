package spec

import (
	"context"
	"fmt"

	"github.com/crestenstclair/crest-spec/internal/observability"
)

func (s *Spec) observationContext(ctx context.Context) (*ProjectOverviewResult, *observability.Service, error) {
	overview, err := s.ProjectOverview(ctx)
	if err != nil {
		return nil, nil, err
	}
	return overview, observability.NewService(s.store), nil
}

func (s *Spec) ObserveProject(ctx context.Context) (*observability.ProjectOverview, error) {
	overview, service, err := s.observationContext(ctx)
	if err != nil {
		return nil, err
	}
	return service.Project(ctx, overview.Project.ProjectName, overview.CompletionEnforced)
}

func (s *Spec) ObserveGoals(ctx context.Context, request observability.PageRequest) (*observability.Page[observability.GoalSummary], error) {
	overview, service, err := s.observationContext(ctx)
	if err != nil {
		return nil, err
	}
	return service.Goals(ctx, overview.Project.ProjectName, request)
}

func (s *Spec) ObserveGoal(ctx context.Context, id string) (*observability.GoalDetail, error) {
	overview, service, err := s.observationContext(ctx)
	if err != nil {
		return nil, err
	}
	return service.Goal(ctx, overview.Project.ProjectName, id)
}

func (s *Spec) ObserveCapabilities(ctx context.Context, request observability.PageRequest) (*observability.Page[observability.CapabilitySummary], error) {
	overview, service, err := s.observationContext(ctx)
	if err != nil {
		return nil, err
	}
	return service.Capabilities(ctx, overview.Project.ProjectName, request)
}

func (s *Spec) ObserveCapability(ctx context.Context, id string) (*observability.CapabilityDetail, error) {
	overview, service, err := s.observationContext(ctx)
	if err != nil {
		return nil, err
	}
	return service.Capability(ctx, overview.Project.ProjectName, id)
}

func (s *Spec) ObserveResources(ctx context.Context, request observability.PageRequest) (*observability.Page[observability.ResourceSummary], error) {
	overview, service, err := s.observationContext(ctx)
	if err != nil {
		return nil, err
	}
	return service.Resources(ctx, overview.Project.ProjectName, request)
}

func (s *Spec) ObserveResource(ctx context.Context, id string) (*observability.ResourceDetail, error) {
	overview, service, err := s.observationContext(ctx)
	if err != nil {
		return nil, err
	}
	return service.Resource(ctx, overview.Project.ProjectName, id)
}

func (s *Spec) ObservePlan(ctx context.Context) (*observability.PlanView, error) {
	overview, service, err := s.observationContext(ctx)
	if err != nil {
		return nil, err
	}
	plan, err := s.Plan(ctx)
	if err != nil {
		return nil, fmt.Errorf("observe plan: %w", err)
	}
	return service.Plan(ctx, overview.Project.ProjectName, overview.Project.SpecHash, plan.Actions, plan.Waves, plan.Slices)
}

func (s *Spec) ObserveAttempt(ctx context.Context, id string) (*observability.AttemptDetail, error) {
	return observability.NewService(s.store).Attempt(ctx, id)
}

func (s *Spec) CompareAttempts(ctx context.Context, left, right string) (*observability.AttemptComparison, error) {
	return observability.NewService(s.store).CompareAttempts(ctx, left, right)
}

func (s *Spec) ObserveFailures(ctx context.Context, request observability.PageRequest) (*observability.Page[observability.FailureSummary], error) {
	return observability.NewService(s.store).Failures(ctx, request)
}

func (s *Spec) ObserveFailure(ctx context.Context, id string) (*observability.FailureDetail, error) {
	return observability.NewService(s.store).Failure(ctx, id)
}
