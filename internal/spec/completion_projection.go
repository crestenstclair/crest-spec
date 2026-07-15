package spec

import (
	"context"
	"fmt"

	"github.com/crestenstclair/crest-spec/internal/store"
)

// completionProjectionFacts is the immutable input to pure completion
// derivation. Canonical reads happen before this value is constructed.
type completionProjectionFacts struct {
	plan              *PlanResult
	intent            *store.ProjectIntentState
	verification      *store.VerificationCompletionFacts
	definitions       []store.VerificationDefinition
	acceptedResources []store.Resource
}

// reprojectCompletion derives lifecycle state only from accepted resources,
// active plans, and canonical SQLite verification facts. Hosts and generators
// never submit completion status directly.
func (s *Spec) reprojectCompletion(ctx context.Context, planResult *PlanResult, source store.TransitionSource) error {
	if planResult == nil || planResult.Registry == nil || planResult.Registry.Project == nil {
		return fmt.Errorf("project completion: plan registry is required")
	}
	facts, err := s.loadCompletionProjectionFacts(ctx, planResult)
	if err != nil {
		return err
	}
	projection := deriveCompletionProjection(facts, indexCompletionProjectionFacts(facts))
	return s.persistCompletionProjection(ctx, facts, projection, source)
}

// loadCompletionProjectionFacts performs canonical reads in the established
// error order. No lifecycle decision is made while persistence is being read.
func (s *Spec) loadCompletionProjectionFacts(ctx context.Context, planResult *PlanResult) (*completionProjectionFacts, error) {
	projectName := planResult.Registry.Project.Name
	intent, err := s.store.GetProjectIntent(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("project completion: intent: %w", err)
	}
	verification, err := s.store.GetVerificationCompletionFacts(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("project completion: verification facts: %w", err)
	}
	definitions, err := s.store.ListVerificationDefinitions(ctx, projectName)
	if err != nil {
		return nil, fmt.Errorf("project completion: definitions: %w", err)
	}
	acceptedResources, err := s.store.ListResources()
	if err != nil {
		return nil, fmt.Errorf("project completion: resources: %w", err)
	}
	return &completionProjectionFacts{
		plan: planResult, intent: intent, verification: verification,
		definitions: definitions, acceptedResources: acceptedResources,
	}, nil
}

func (s *Spec) persistCompletionProjection(
	ctx context.Context,
	facts *completionProjectionFacts,
	projection completionProjectionResult,
	source store.TransitionSource,
) error {
	return s.store.ApplyCompletionProjection(
		ctx, facts.plan.Registry.Project.Name, projection.goals, projection.project, source,
	)
}

func (s *Spec) refreshCompletion(ctx context.Context, source store.TransitionSource) error {
	planResult, err := s.Plan(ctx)
	if err != nil {
		return err
	}
	return s.reprojectCompletion(ctx, planResult, source)
}
