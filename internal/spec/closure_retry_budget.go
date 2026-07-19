package spec

import (
	"context"
	"fmt"
)

// enforceAffectedClosureRepairBudget is the final dispatch-side guard for the
// durable wave retry policy. The closure router normally leaves an exhausted
// owner committed, but this independent check also rejects legacy projections,
// a host racing with validation bookkeeping, and state restored after restart.
//
// A count of one is repair retry 1. Therefore a configured maximum of two
// permits counts one and two and rejects the third distinct accepted-baseline
// failure. Zero preserves the historical unlimited behavior for embeddings
// that construct Config directly without setting WaveMaxRetries.
func (s *Spec) enforceAffectedClosureRepairBudget(ctx context.Context, sessionID, resourceID string) error {
	if s.cfg == nil || s.cfg.WaveMaxRetries <= 0 {
		return nil
	}
	count, err := s.store.CountAffectedClosureRepairCycles(ctx, sessionID, resourceID)
	if err != nil {
		return fmt.Errorf("check affected-closure repair retry limit: %w", err)
	}
	if count <= s.cfg.WaveMaxRetries {
		return nil
	}
	return fmt.Errorf(
		"resource %s exhausted affected-closure repair retries in session %s: retry_count=%d max_retries=%d; corrective_action=triage_or_amend_spec",
		resourceID, sessionID, count, s.cfg.WaveMaxRetries,
	)
}

func (s *Spec) affectedClosureRetryStatus(ctx context.Context, sessionID string) (retryCount, maxRetries int, err error) {
	if s.cfg != nil {
		maxRetries = s.cfg.WaveMaxRetries
	}
	retryCount, err = s.store.MaxAffectedClosureRepairCycles(ctx, sessionID)
	if err != nil {
		return 0, maxRetries, fmt.Errorf("load affected-closure repair retry status: %w", err)
	}
	return retryCount, maxRetries, nil
}
