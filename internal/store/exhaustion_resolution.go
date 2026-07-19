package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ResolveSessionResource records host guidance and clears a resolvable
// resource projection. When affected-closure routing linked an exhausted,
// handoffless integration failure, the classification and resource projection
// are resolved in the same transaction. This keeps spec/resolve as the single
// host action while preventing an unrelated or older classification from being
// treated as the exhaustion that is currently projected.
func (s *Store) ResolveSessionResource(
	ctx context.Context,
	sessionID, resourceID, guidance string,
) error {
	sessionID = strings.TrimSpace(sessionID)
	resourceID = strings.TrimSpace(resourceID)
	guidance = strings.TrimSpace(guidance)
	if sessionID == "" || resourceID == "" || guidance == "" {
		return fmt.Errorf("resolve session resource: session id, resource id, and guidance are required")
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("resolve session resource: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var status, state, phase, exhaustionFailureID string
	err = tx.QueryRowContext(ctx, `
		SELECT session.status, resource.state, resource.phase,
		       COALESCE(resource.exhaustion_failure_id, '')
		FROM agent_sessions AS session
		JOIN session_resources AS resource ON resource.session_id = session.id
		WHERE session.id = ? AND resource.resource_id = ?`, sessionID, resourceID,
	).Scan(&status, &state, &phase, &exhaustionFailureID)
	if err == sql.ErrNoRows {
		return fmt.Errorf("resolve session resource: resource not found")
	}
	if err != nil {
		return fmt.Errorf("resolve session resource: inspect projection: %w", err)
	}
	if status != "active" {
		return fmt.Errorf("resolve session resource: session %s is %s", sessionID, status)
	}
	if state != "pending" && state != "blocked" && state != "errored" &&
		state != "timed_out" && state != "rejected" {
		return fmt.Errorf("resolve session resource: resource %s is not resolvable from state %s", resourceID, state)
	}

	if exhaustionFailureID != "" {
		var (
			failureResolution, resolvedAt, resolvedByAttempt                                  string
			failureHandoffs, unresolvedCompetitors, laterClosureFailures, unsafeNewerAttempts int
		)
		err = tx.QueryRowContext(ctx, `
			SELECT failure.resolution, COALESCE(failure.resolved_at, ''),
			       COALESCE(failure.resolved_by_attempt, ''),
			       (SELECT COUNT(*) FROM attempt_handoffs AS handoff
			        WHERE handoff.failure_id = failure.id),
			       (
			           SELECT COUNT(*)
			           FROM failure_classifications AS competing_failure
			           JOIN generation_attempts AS competing_attempt
			             ON competing_attempt.id = competing_failure.attempt_id
			           WHERE competing_attempt.session_id = attempt.session_id
			             AND competing_attempt.resource_id = attempt.resource_id
			             AND competing_failure.resource_id = failure.resource_id
			             AND competing_failure.category = 'integration_error'
			             AND competing_failure.origin = 'engine'
			             AND competing_failure.evidence_source = 'affected_closure_validation'
			             AND competing_failure.resolved_at IS NULL
			             AND competing_failure.id != failure.id
			       ),
			       (
			           SELECT COUNT(*)
			           FROM failure_classifications AS later_failure
			           JOIN generation_attempts AS later_attempt
			             ON later_attempt.id = later_failure.attempt_id
			           WHERE later_attempt.session_id = attempt.session_id
			             AND later_attempt.resource_id = attempt.resource_id
			             AND later_failure.resource_id = failure.resource_id
			             AND later_failure.origin = 'engine'
			             AND later_failure.evidence_source = 'affected_closure_validation'
			             AND (
			                 later_failure.created_at > failure.created_at
			                 OR (later_failure.created_at = failure.created_at AND later_failure.id > failure.id)
			             )
			       ),
			       (
			           SELECT COUNT(*)
			           FROM generation_attempts AS newer
			           WHERE newer.session_id = attempt.session_id
			             AND newer.resource_id = attempt.resource_id
			             AND newer.retry_number > attempt.retry_number
			             AND (
			                 newer.status != 'abandoned'
			                 OR EXISTS (SELECT 1 FROM execution_manifests AS execution
			                            WHERE execution.attempt_id = newer.id)
			                 OR EXISTS (SELECT 1 FROM candidate_sets AS candidate
			                            WHERE candidate.attempt_id = newer.id)
			             )
			       )
			FROM failure_classifications AS failure
			JOIN generation_attempts AS attempt ON attempt.id = failure.attempt_id
			JOIN execution_manifests AS execution ON execution.attempt_id = attempt.id
			JOIN candidate_sets AS candidate ON candidate.attempt_id = attempt.id
			WHERE failure.id = ?
			  AND attempt.session_id = ? AND attempt.resource_id = ?
			  AND attempt.status = 'accepted' AND execution.status = 'accepted'
			  AND candidate.status = 'accepted'
			  AND failure.resource_id = ?
			  AND failure.category = 'integration_error'
			  AND failure.origin = 'engine'
			  AND failure.evidence_source = 'affected_closure_validation'`,
			exhaustionFailureID, sessionID, resourceID, resourceID,
		).Scan(
			&failureResolution, &resolvedAt, &resolvedByAttempt,
			&failureHandoffs, &unresolvedCompetitors, &laterClosureFailures, &unsafeNewerAttempts,
		)
		if err == sql.ErrNoRows {
			return fmt.Errorf("resolve session resource: exhaustion provenance is not eligible")
		}
		if err != nil {
			return fmt.Errorf("resolve session resource: inspect exhaustion provenance: %w", err)
		}
		if phase != "integration_failed" || failureHandoffs != 0 ||
			unresolvedCompetitors != 0 || laterClosureFailures != 0 ||
			unsafeNewerAttempts != 0 || resolvedByAttempt != "" {
			return fmt.Errorf(
				"resolve session resource: exhaustion provenance is ambiguous: phase=%q handoffs=%d unresolved_competitors=%d later_closure_failures=%d unsafe_newer=%d resolved_by_attempt=%t",
				phase, failureHandoffs, unresolvedCompetitors, laterClosureFailures,
				unsafeNewerAttempts, resolvedByAttempt != "",
			)
		}
		if resolvedAt == "" {
			timestamp := now()
			result, updateErr := tx.ExecContext(ctx, `
				UPDATE failure_classifications
				SET resolution = ?, resolved_by_attempt = NULL, resolved_at = ?
				WHERE id = ? AND resolved_at IS NULL`, guidance, timestamp, exhaustionFailureID)
			if updateErr != nil {
				return fmt.Errorf("resolve session resource: resolve exhaustion failure: %w", updateErr)
			}
			changed, rowsErr := result.RowsAffected()
			if rowsErr != nil || changed != 1 {
				return fmt.Errorf("resolve session resource: resolve exhaustion failure affected=%d: %w", changed, rowsErr)
			}
		} else if failureResolution == "" {
			return fmt.Errorf("resolve session resource: resolved exhaustion failure has no resolution")
		}
	}

	timestamp := now()
	result, err := tx.ExecContext(ctx, `
		UPDATE session_resources
		SET state = 'pending', last_error = NULL, last_output = NULL,
		    job_id = NULL, updated_at = ?
		WHERE session_id = ? AND resource_id = ?
		  AND state IN ('pending','blocked','errored','timed_out','rejected')`,
		timestamp, sessionID, resourceID,
	)
	if err != nil {
		return fmt.Errorf("resolve session resource: update projection: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("resolve session resource: update projection affected=%d: %w", changed, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("resolve session resource: commit: %w", err)
	}
	return nil
}
