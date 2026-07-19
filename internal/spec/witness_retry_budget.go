package spec

import (
	"context"
	"fmt"
)

// enforceBehavioralWitnessRepairBudget is the scheduler-side half of durable
// witness routing. An exhausted content/definition epoch remains visible as an
// errored resource, but cannot consume another worker or model attempt. A new
// accepted candidate or active witness definition has a different epoch and
// therefore is not blocked by stale exhaustion history.
func (s *Spec) enforceBehavioralWitnessRepairBudget(
	ctx context.Context,
	sessionID, resourceID string,
) error {
	exhausted, err := s.store.ActiveBehavioralWitnessRepairExhaustion(ctx, sessionID, resourceID)
	if err != nil {
		return fmt.Errorf("inspect behavioral-witness repair budget: %w", err)
	}
	if exhausted == nil {
		return nil
	}
	return fmt.Errorf(
		"resource %s exhausted behavioral-witness repair retries in session %s: witness=%s retry_count=%d max_retries=%d source_kind=%s; change the source candidate or witness definition before retrying",
		resourceID, sessionID, exhausted.WitnessID, exhausted.RetryCount,
		exhausted.MaxRetries, exhausted.SourceKind,
	)
}
