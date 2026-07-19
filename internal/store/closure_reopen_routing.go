package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/db"
	"github.com/crestenstclair/crest-spec/internal/execution"
)

// ClosureValidationFailureRoutingInput describes one affected-closure
// observation. MaxRepairRetries is a durable repair-cycle cap; zero preserves
// uncapped behavior for direct callers and legacy tests.
type ClosureValidationFailureRoutingInput struct {
	SessionID        string
	ResourceIDs      []string
	Reason           string
	Category         execution.FailureCategory
	MaxRepairRetries int

	// transitionHook is a package-local crash/concurrency seam. Production
	// callers cannot set it; storage tests use it to stop at transaction
	// boundaries and prove that uncommitted eligibility is never observable.
	transitionHook func(stage, resourceID string) error
}

type ClosureValidationFailureRoutingResult struct {
	ReopenedResources  []string
	ExhaustedResources []string
	RetryCounts        map[string]int
	Failures           []FailureClassification
	Handoffs           []AttemptHandoff
}

// RouteClosureValidationFailures makes repair eligibility and its complete
// provenance visible in one SQLite commit. A generated owner cannot become
// pending unless its accepted attempt, accepted execution, accepted candidate,
// engine failure classification, and integration-repair handoff are all
// durable. Any error rolls the complete owner batch back to its prior
// committed projection.
func (s *Store) RouteClosureValidationFailures(
	ctx context.Context,
	input ClosureValidationFailureRoutingInput,
) (*ClosureValidationFailureRoutingResult, error) {
	return s.routeClosureValidationFailures(ctx, input)
}

func (s *Store) routeClosureValidationFailures(
	ctx context.Context,
	input ClosureValidationFailureRoutingInput,
) (*ClosureValidationFailureRoutingResult, error) {
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.SessionID == "" || input.Reason == "" {
		return nil, fmt.Errorf("route closure validation failures: session id and reason are required")
	}
	if err := execution.ValidateFailureCategory(input.Category); err != nil {
		return nil, fmt.Errorf("route closure validation failures: %w", err)
	}
	route := execution.RouteFailure(input.Category)
	if route.BlocksRetry || route.NextRole == "" {
		return nil, fmt.Errorf(
			"route closure validation failures: category %s has no executable repair route",
			input.Category,
		)
	}
	ids := append([]string(nil), input.ResourceIDs...)
	sort.Strings(ids)
	ids = uniqueStrings(ids)
	result := &ClosureValidationFailureRoutingResult{RetryCounts: make(map[string]int)}
	if len(ids) == 0 {
		return result, nil
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("route closure validation failures: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	timestamp := now()

	attempts, err := q.ListGenerationAttemptsBySession(ctx, input.SessionID)
	if err != nil {
		return nil, fmt.Errorf("route closure validation failures: list accepted attempts: %w", err)
	}
	latestAccepted := make(map[string]db.GenerationAttempt)
	for _, attempt := range attempts {
		if attempt.Status != "accepted" {
			continue
		}
		current, exists := latestAccepted[attempt.ResourceID]
		if !exists || attempt.RetryNumber > current.RetryNumber ||
			(attempt.RetryNumber == current.RetryNumber && attempt.ID > current.ID) {
			latestAccepted[attempt.ResourceID] = attempt
		}
	}

	for _, resourceID := range ids {
		resource, getErr := q.GetSessionResource(ctx, db.GetSessionResourceParams{
			SessionID: input.SessionID, ResourceID: resourceID,
		})
		if getErr == sql.ErrNoRows {
			return nil, fmt.Errorf("route closure validation failures: resource %s is not in session", resourceID)
		}
		if getErr != nil {
			return nil, fmt.Errorf("route closure validation failures: inspect %s: %w", resourceID, getErr)
		}
		if resource.State != "committed" {
			// A response-loss replay after the canonical commit is a no-op. The
			// already-pending owner retains the route written by that commit.
			continue
		}
		if resource.ExecutionStrategy == "validate_existing" || resource.ExecutionStrategy == "reuse_candidate" {
			// Fast paths deliberately have no generation attempt in this session,
			// so there is no source attempt on which a truthful classification or
			// handoff can be recorded. Downgrade the owner to generation in the
			// same transaction as every generated owner in this closure batch. A
			// concurrent scheduler therefore sees either the old committed fast
			// path or the complete pending repair batch, never an unrouted middle.
			updated, updateErr := tx.ExecContext(ctx, `
				UPDATE session_resources
				SET state = 'pending', phase = 'integration_failed', execution_strategy = 'generate',
				    source_candidate_id = NULL, exhaustion_failure_id = NULL,
				    last_error = ?, attempts = 0,
				    job_id = NULL, updated_at = ?
				WHERE session_id = ? AND resource_id = ? AND state = 'committed'`,
				input.Reason, timestamp, input.SessionID, resourceID,
			)
			if updateErr != nil {
				return nil, fmt.Errorf("route closure validation failures: downgrade fast path %s: %w", resourceID, updateErr)
			}
			changed, rowsErr := updated.RowsAffected()
			if rowsErr != nil || changed != 1 {
				return nil, fmt.Errorf(
					"route closure validation failures: downgrade fast path %s affected=%d: %w",
					resourceID, changed, rowsErr,
				)
			}
			result.ReopenedResources = append(result.ReopenedResources, resourceID)
			if err := runClosureRoutingHook(input, "after_reopen", resourceID); err != nil {
				return nil, err
			}
			continue
		}
		if resource.ExecutionStrategy != "generate" {
			return nil, fmt.Errorf(
				"route closure validation failures: resource %s has no accepted attempt provenance for strategy %s; preserving committed state",
				resourceID, resource.ExecutionStrategy,
			)
		}
		attempt, exists := latestAccepted[resourceID]
		if !exists {
			return nil, fmt.Errorf(
				"route closure validation failures: resource %s has no accepted attempt provenance; preserving committed state",
				resourceID,
			)
		}
		manifest, manifestErr := q.GetExecutionManifestByAttempt(ctx, attempt.ID)
		if manifestErr != nil || manifest.Status != string(execution.StatusAccepted) {
			return nil, fmt.Errorf(
				"route closure validation failures: resource %s accepted attempt has no accepted execution: %w",
				resourceID, manifestErr,
			)
		}
		candidate, candidateErr := q.GetCandidateSetByAttempt(ctx, attempt.ID)
		if candidateErr != nil || candidate.Status != "accepted" || candidate.ExecutionID != manifest.ID {
			return nil, fmt.Errorf(
				"route closure validation failures: resource %s accepted attempt has no matching accepted candidate: %w",
				resourceID, candidateErr,
			)
		}

		failure, failureErr := createFailureClassificationTx(ctx, q, FailureClassificationWrite{
			AttemptID: attempt.ID, ExecutionID: manifest.ID, Category: input.Category,
			Origin: "engine", Confidence: 1, EvidenceSource: "affected_closure_validation",
			EvidenceReference: input.SessionID, Evidence: input.Reason,
		}, timestamp)
		if failureErr != nil {
			return nil, fmt.Errorf("route closure validation failures: classify %s: %w", resourceID, failureErr)
		}
		result.Failures = append(result.Failures, *failure)
		if err := runClosureRoutingHook(input, "after_failure", resourceID); err != nil {
			return nil, err
		}

		retryCountValue, countErr := q.CountAffectedClosureRepairCyclesBySessionResource(
			ctx, db.CountAffectedClosureRepairCyclesBySessionResourceParams{
				InputSessionID: input.SessionID, InputResourceID: resourceID,
			},
		)
		if countErr != nil {
			return nil, fmt.Errorf("route closure validation failures: count repair cycles for %s: %w", resourceID, countErr)
		}
		retryCount := int(retryCountValue)
		result.RetryCounts[resourceID] = retryCount
		if input.MaxRepairRetries > 0 && retryCount > input.MaxRepairRetries {
			exhaustedReason := fmt.Sprintf(
				"affected-closure repair retries exhausted: retry_count=%d max_retries=%d; corrective_action=triage_or_amend_spec",
				retryCount, input.MaxRepairRetries,
			)
			updated, updateErr := tx.ExecContext(ctx, `
				UPDATE session_resources
				SET state = 'errored', phase = 'integration_failed', last_error = ?,
				    job_id = NULL, exhaustion_failure_id = ?, updated_at = ?
				WHERE session_id = ? AND resource_id = ? AND state = 'committed'`,
				exhaustedReason, failure.ID, timestamp, input.SessionID, resourceID,
			)
			if updateErr != nil {
				return nil, fmt.Errorf("route closure validation failures: exhaust %s: %w", resourceID, updateErr)
			}
			changed, rowsErr := updated.RowsAffected()
			if rowsErr != nil || changed != 1 {
				return nil, fmt.Errorf(
					"route closure validation failures: exhaust %s affected=%d: %w",
					resourceID, changed, rowsErr,
				)
			}
			result.ExhaustedResources = append(result.ExhaustedResources, resourceID)
			if err := runClosureRoutingHook(input, "after_exhausted", resourceID); err != nil {
				return nil, err
			}
			continue
		}

		// This handoff is an engine-owned routing decision, not an action by
		// the source worker. It intentionally does not apply source-role policy;
		// the failure taxonomy's target role is the authoritative route.
		handoff := AttemptHandoff{
			ID: uuid.NewString(), SourceAttemptID: attempt.ID, SourceRole: attempt.Role,
			TargetRole: string(route.NextRole), Reason: input.Reason,
			ExpectedOutcome: "repair every diagnostic attributed to this resource",
			FailureID:       failure.ID, Status: "pending", CreatedAt: parseTime(timestamp),
		}
		if err := q.CreateAttemptHandoff(ctx, db.CreateAttemptHandoffParams{
			ID: handoff.ID, SourceAttemptID: handoff.SourceAttemptID,
			SourceRole: handoff.SourceRole, TargetRole: handoff.TargetRole,
			Reason: handoff.Reason, ExpectedOutcome: handoff.ExpectedOutcome,
			FailureID: &handoff.FailureID, Status: handoff.Status, CreatedAt: timestamp,
		}); err != nil {
			return nil, fmt.Errorf("route closure validation failures: create handoff for %s: %w", resourceID, err)
		}
		result.Handoffs = append(result.Handoffs, handoff)
		if err := runClosureRoutingHook(input, "after_handoff", resourceID); err != nil {
			return nil, err
		}

		updated, updateErr := tx.ExecContext(ctx, `
			UPDATE session_resources
			SET state = 'pending', phase = 'integration_failed', execution_strategy = 'generate',
			    source_candidate_id = NULL, exhaustion_failure_id = NULL,
			    last_error = ?, attempts = 0,
			    job_id = NULL, updated_at = ?
			WHERE session_id = ? AND resource_id = ? AND state = 'committed'`,
			input.Reason, timestamp, input.SessionID, resourceID,
		)
		if updateErr != nil {
			return nil, fmt.Errorf("route closure validation failures: reopen %s: %w", resourceID, updateErr)
		}
		changed, rowsErr := updated.RowsAffected()
		if rowsErr != nil || changed != 1 {
			return nil, fmt.Errorf(
				"route closure validation failures: reopen %s affected=%d: %w",
				resourceID, changed, rowsErr,
			)
		}
		result.ReopenedResources = append(result.ReopenedResources, resourceID)
		if err := runClosureRoutingHook(input, "after_reopen", resourceID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("route closure validation failures: commit: %w", err)
	}
	sort.Strings(result.ReopenedResources)
	sort.Strings(result.ExhaustedResources)
	return result, nil
}

func runClosureRoutingHook(
	input ClosureValidationFailureRoutingInput,
	stage, resourceID string,
) error {
	if input.transitionHook == nil {
		return nil
	}
	if err := input.transitionHook(stage, resourceID); err != nil {
		return fmt.Errorf("route closure validation failures: %s for %s: %w", stage, resourceID, err)
	}
	return nil
}
