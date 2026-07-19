package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"

	cserrors "github.com/crestenstclair/crest-spec/internal/errors"
)

// SessionAbandonResult reports the durable state transitions performed while
// abandoning a session. Accepted candidates and committed resource
// projections are never rolled back by this operation.
type SessionAbandonResult struct {
	SessionID            string
	Status               string
	Reason               string
	ResourcesSkipped     int
	AttemptsAbandoned    int
	ExecutionsCancelled  int
	CandidatesRejected   int
	ApplyCancelled       bool
	LockReleased         bool
	CommittedInvalidated int
}

// AbandonSession atomically terminates unfinished work for a session. The
// singleton lock is released only when this session is still its holder.
// Accepted file history and ownership stay intact. Provisional committed
// resources are always invalidated so an abandoned session cannot publish
// unverified global resource state; invalidateCommitted additionally reopens
// resources that had already reached the verified phase.
func (s *Store) AbandonSession(ctx context.Context, sessionID, reason string, invalidateCommitted bool) (*SessionAbandonResult, error) {
	sessionID = strings.TrimSpace(sessionID)
	reason = strings.TrimSpace(reason)
	if sessionID == "" {
		return nil, fmt.Errorf("abandon session: session id is required")
	}
	if reason == "" {
		return nil, fmt.Errorf("abandon session: reason is required")
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("abandon session: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	timestamp := now()
	updated, err := tx.ExecContext(ctx, `
		UPDATE agent_sessions
		SET status = 'aborted', updated_at = ?
		WHERE id = ? AND status = 'active'`, timestamp, sessionID)
	if err != nil {
		return nil, fmt.Errorf("abandon session: mark aborted: %w", err)
	}
	changed, err := updated.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("abandon session: inspect session update: %w", err)
	}

	var applyID, status string
	if err := tx.QueryRowContext(ctx,
		`SELECT apply_id, status FROM agent_sessions WHERE id = ?`, sessionID,
	).Scan(&applyID, &status); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("abandon session: %w", cserrors.ErrNotFound)
		}
		return nil, fmt.Errorf("abandon session: load session: %w", err)
	}
	if changed == 0 && status != "aborted" {
		return nil, fmt.Errorf("abandon session: session %s has terminal status %s", sessionID, status)
	}

	result := &SessionAbandonResult{SessionID: sessionID, Status: "aborted", Reason: reason}

	// Capture the pre-transition execution states for one audit event per
	// cancelled execution. The session update above already holds SQLite's
	// write lock, so these states cannot race inside this transaction.
	type inFlightExecution struct {
		id     string
		status string
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT em.id, em.status
		FROM execution_manifests em
		JOIN generation_attempts ga ON ga.id = em.attempt_id
		WHERE ga.session_id = ?
		  AND em.status IN ('executing','candidate_submitted','validating')
		ORDER BY em.id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("abandon session: list in-flight executions: %w", err)
	}
	var executions []inFlightExecution
	for rows.Next() {
		var execution inFlightExecution
		if err := rows.Scan(&execution.id, &execution.status); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("abandon session: scan in-flight execution: %w", err)
		}
		executions = append(executions, execution)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("abandon session: close in-flight executions: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("abandon session: iterate in-flight executions: %w", err)
	}

	candidates, err := tx.ExecContext(ctx, `
		UPDATE candidate_sets
		SET status = 'rejected', disposition_reason = ?, disposed_at = ?
		WHERE status = 'submitted'
		  AND attempt_id IN (SELECT id FROM generation_attempts WHERE session_id = ?)`,
		reason, timestamp, sessionID)
	if err != nil {
		return nil, fmt.Errorf("abandon session: reject submitted candidates: %w", err)
	}
	result.CandidatesRejected, err = affectedCount(candidates)
	if err != nil {
		return nil, fmt.Errorf("abandon session: inspect rejected candidates: %w", err)
	}

	q := s.queries.WithTx(tx)
	for _, execution := range executions {
		cancelled, updateErr := tx.ExecContext(ctx, `
			UPDATE execution_manifests
			SET status = 'cancelled', disposition_reason = ?, completed_at = ?, updated_at = ?
			WHERE id = ? AND status = ?`,
			reason, timestamp, timestamp, execution.id, execution.status)
		if updateErr != nil {
			return nil, fmt.Errorf("abandon session: cancel execution %s: %w", execution.id, updateErr)
		}
		count, countErr := affectedCount(cancelled)
		if countErr != nil {
			return nil, fmt.Errorf("abandon session: inspect cancelled execution %s: %w", execution.id, countErr)
		}
		if count != 1 {
			return nil, fmt.Errorf("abandon session: execution %s changed concurrently", execution.id)
		}
		if eventErr := createExecutionEvent(ctx, q, execution.id, execution.status, "cancelled", "execution_cancelled", map[string]any{
			"reason": reason, "session_id": sessionID, "source": "spec/abandon",
		}, timestamp); eventErr != nil {
			return nil, fmt.Errorf("abandon session: record cancellation for execution %s: %w", execution.id, eventErr)
		}
		result.ExecutionsCancelled++
	}

	attempts, err := tx.ExecContext(ctx, `
		UPDATE generation_attempts
		SET status = 'abandoned'
		WHERE session_id = ? AND status IN ('context_prepared','candidate_submitted')`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("abandon session: abandon attempts: %w", err)
	}
	result.AttemptsAbandoned, err = affectedCount(attempts)
	if err != nil {
		return nil, fmt.Errorf("abandon session: inspect abandoned attempts: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE context_batches
		SET status = 'failed', completed_at = ?, updated_at = ?
		WHERE session_id = ? AND status = 'running'`, timestamp, timestamp, sessionID); err != nil {
		return nil, fmt.Errorf("abandon session: terminate context batches: %w", err)
	}

	resources, err := tx.ExecContext(ctx, `
		UPDATE session_resources
		SET state = 'skipped', phase = 'abandoned', last_error = ?, job_id = NULL, updated_at = ?
		WHERE session_id = ? AND state NOT IN ('committed','skipped')`, reason, timestamp, sessionID)
	if err != nil {
		return nil, fmt.Errorf("abandon session: skip unfinished resources: %w", err)
	}
	result.ResourcesSkipped, err = affectedCount(resources)
	if err != nil {
		return nil, fmt.Errorf("abandon session: inspect skipped resources: %w", err)
	}

	{
		committedRows, queryErr := tx.QueryContext(ctx, `
			SELECT resource_id FROM session_resources
			WHERE session_id = ? AND state = 'committed'
			  AND (? OR phase <> 'verified')
			ORDER BY resource_id`, sessionID, invalidateCommitted)
		if queryErr != nil {
			return nil, fmt.Errorf("abandon session: list committed resources: %w", queryErr)
		}
		var committedIDs []string
		for committedRows.Next() {
			var resourceID string
			if scanErr := committedRows.Scan(&resourceID); scanErr != nil {
				_ = committedRows.Close()
				return nil, fmt.Errorf("abandon session: scan committed resource: %w", scanErr)
			}
			committedIDs = append(committedIDs, resourceID)
		}
		if closeErr := committedRows.Close(); closeErr != nil {
			return nil, fmt.Errorf("abandon session: close committed resources: %w", closeErr)
		}
		if rowsErr := committedRows.Err(); rowsErr != nil {
			return nil, fmt.Errorf("abandon session: iterate committed resources: %w", rowsErr)
		}
		for _, resourceID := range committedIDs {
			marker := abandonedEffectiveHash(sessionID, resourceID)
			invalidated, updateErr := tx.ExecContext(ctx, `
				UPDATE resources SET effective_hash = ?
				WHERE id = ? AND effective_hash <> ?`, marker, resourceID, marker)
			if updateErr != nil {
				return nil, fmt.Errorf("abandon session: invalidate committed resource %s: %w", resourceID, updateErr)
			}
			count, countErr := affectedCount(invalidated)
			if countErr != nil {
				return nil, fmt.Errorf("abandon session: inspect invalidated resource %s: %w", resourceID, countErr)
			}
			result.CommittedInvalidated += count
		}
	}

	apply, err := tx.ExecContext(ctx, `
		UPDATE applies SET status = 'cancelled', done_at = ?
		WHERE id = ? AND status = 'running'`, timestamp, applyID)
	if err != nil {
		return nil, fmt.Errorf("abandon session: cancel apply: %w", err)
	}
	applyCount, err := affectedCount(apply)
	if err != nil {
		return nil, fmt.Errorf("abandon session: inspect cancelled apply: %w", err)
	}
	result.ApplyCancelled = applyCount == 1

	lock, err := tx.ExecContext(ctx, `DELETE FROM lock WHERE id = 1 AND holder = ?`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("abandon session: release owned lock: %w", err)
	}
	lockCount, err := affectedCount(lock)
	if err != nil {
		return nil, fmt.Errorf("abandon session: inspect released lock: %w", err)
	}
	result.LockReleased = lockCount == 1

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("abandon session: commit: %w", err)
	}
	return result, nil
}

func affectedCount(result sql.Result) (int, error) {
	count, err := result.RowsAffected()
	return int(count), err
}

func abandonedEffectiveHash(sessionID, resourceID string) string {
	sum := sha256.Sum256([]byte("crest-spec:abandoned:v1\x00" + sessionID + "\x00" + resourceID))
	return fmt.Sprintf("abandoned:%x", sum)
}
