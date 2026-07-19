package spec

import (
	"context"
	"fmt"
	"strings"
)

// AbandonResult describes the durable cleanup of an interrupted generation
// session. Committed files and ownership are retained even when their hashes
// are invalidated for a targeted re-plan.
type AbandonResult struct {
	SessionID            string `json:"session_id"`
	Status               string `json:"status"`
	Reason               string `json:"reason"`
	ResourcesSkipped     int    `json:"resources_skipped"`
	AttemptsAbandoned    int    `json:"attempts_abandoned"`
	ExecutionsCancelled  int    `json:"executions_cancelled"`
	CandidatesRejected   int    `json:"candidates_rejected"`
	ApplyCancelled       bool   `json:"apply_cancelled"`
	LockReleased         bool   `json:"lock_released"`
	CommittedInvalidated int    `json:"committed_invalidated"`
}

// Abandon terminates unfinished work without reverting accepted history.
// Provisional committed resources are always marked stale so they cannot skip
// closure validation. invalidateCommitted additionally marks already-verified
// resources stale for a subsequent repair against their existing files.
func (s *Spec) Abandon(ctx context.Context, sessionID, reason string, invalidateCommitted bool) (*AbandonResult, error) {
	sessionID = strings.TrimSpace(sessionID)
	reason = strings.TrimSpace(reason)
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	if reason == "" {
		return nil, fmt.Errorf("reason is required")
	}

	result, err := s.store.AbandonSession(ctx, sessionID, reason, invalidateCommitted)
	if err != nil {
		return nil, err
	}
	return &AbandonResult{
		SessionID:            result.SessionID,
		Status:               result.Status,
		Reason:               result.Reason,
		ResourcesSkipped:     result.ResourcesSkipped,
		AttemptsAbandoned:    result.AttemptsAbandoned,
		ExecutionsCancelled:  result.ExecutionsCancelled,
		CandidatesRejected:   result.CandidatesRejected,
		ApplyCancelled:       result.ApplyCancelled,
		LockReleased:         result.LockReleased,
		CommittedInvalidated: result.CommittedInvalidated,
	}, nil
}
