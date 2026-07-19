package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/crestenstclair/crest-spec/internal/db"
	"github.com/crestenstclair/crest-spec/internal/execution"
)

const (
	PreExecutionRecoveryEvidenceSource = "pre_execution_context_recovery"
	DispatchFailureEvidenceSource      = "host_dispatch_failure"
)

// DispatchFailure is an exact host report that an assigned worker could not
// reach execution registration. The stable identities are part of the CAS;
// they are not advisory metadata.
type DispatchFailure struct {
	ID            string
	SessionID     string
	ResourceID    string
	HostSessionID string
	WorkerID      string
	Reason        string
}

// PreparedContextAbandonment describes an engine-owned invalidation of a
// context that never reached execution registration. Before is optional: when
// set, activity at or after the cutoff wins the race and leaves the attempt
// untouched. Omitting it supports immediate invalidation after source
// provenance detects a stale snapshot.
type PreparedContextAbandonment struct {
	AttemptID      string
	Reason         string
	EvidenceSource string
	Before         *time.Time
}

// PreparedContextRecovery reports the result of one compare-and-swap recovery.
// Recovered=false is a benign race: the attempt started executing, was already
// recovered, or received lifecycle activity at/after the requested cutoff.
type PreparedContextRecovery struct {
	AttemptID           string
	SessionID           string
	ResourceID          string
	DispatchLifecycleID string
	FailureID           string
	Recovered           bool
	ClaimReleased       bool
}

// FailDispatch immediately releases an exact claimed attempt that has not
// registered an execution. The attempt abandonment, claim release, retry
// projection, and immutable recovery classification commit atomically.
func (s *Store) FailDispatch(ctx context.Context, input DispatchFailure) (*DispatchLifecycle, error) {
	input.ID = strings.TrimSpace(input.ID)
	input.SessionID = strings.TrimSpace(input.SessionID)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	input.HostSessionID = strings.TrimSpace(input.HostSessionID)
	input.WorkerID = strings.TrimSpace(input.WorkerID)
	input.Reason = strings.TrimSpace(input.Reason)
	if input.ID == "" || input.SessionID == "" || input.ResourceID == "" ||
		input.HostSessionID == "" || input.WorkerID == "" {
		return nil, fmt.Errorf("fail dispatch: dispatch_id, session_id, resource_id, host_session_id, and worker_id are required")
	}
	if input.Reason == "" {
		return nil, fmt.Errorf("fail dispatch: reason is required")
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("fail dispatch: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)
	row, err := q.GetDispatchLifecycle(ctx, input.ID)
	if err != nil {
		return nil, fmt.Errorf("fail dispatch: lifecycle: %w", mapNotFound(err))
	}
	lifecycle := dbDispatchLifecycle(row)
	if lifecycle.SessionID != input.SessionID || lifecycle.ResourceID != input.ResourceID {
		return nil, fmt.Errorf("fail dispatch: dispatch lifecycle belongs to another session resource")
	}
	if lifecycle.HostSessionID != input.HostSessionID || lifecycle.WorkerID != input.WorkerID {
		return nil, fmt.Errorf("fail dispatch: lifecycle identity does not match the exact host worker assignment")
	}
	if lifecycle.AttemptID == "" || lifecycle.DispatchRequestedAt == nil {
		return nil, fmt.Errorf("fail dispatch: dispatch_requested must be recorded for a prepared attempt")
	}
	if lifecycle.ExecutionRegisteredAt != nil {
		return nil, fmt.Errorf("fail dispatch: execution already registered; dispatch_failed cannot cancel it")
	}

	auditKey := db.GetDispatchFailureAuditIDParams{AttemptID: lifecycle.AttemptID, DispatchID: lifecycle.ID}
	if _, auditErr := q.GetDispatchFailureAuditID(ctx, auditKey); auditErr == nil {
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("fail dispatch: commit replay: %w", err)
		}
		return &lifecycle, nil
	} else if !errors.Is(auditErr, sql.ErrNoRows) {
		return nil, fmt.Errorf("fail dispatch: inspect replay audit: %w", auditErr)
	}

	changed, err := q.AbandonFailedDispatchAttempt(ctx, db.AbandonFailedDispatchAttemptParams{
		AttemptID: lifecycle.AttemptID, DispatchID: lifecycle.ID,
		SessionID: input.SessionID, ResourceID: input.ResourceID,
		HostSessionID: input.HostSessionID, WorkerID: input.WorkerID,
	})
	if err != nil {
		return nil, fmt.Errorf("fail dispatch: abandon attempt: %w", err)
	}
	if changed != 1 {
		_ = tx.Rollback()
		return s.failedDispatchRaceResult(ctx, input, lifecycle)
	}

	timestamp := now()
	released, err := q.ReleaseRecoveredPreExecutionClaim(ctx, db.ReleaseRecoveredPreExecutionClaimParams{
		LastError: stringPtr(input.Reason), UpdatedAt: timestamp,
		SessionID: input.SessionID, ResourceID: input.ResourceID,
		JobID: stringPtr(lifecycle.AttemptID),
	})
	if err != nil {
		return nil, fmt.Errorf("fail dispatch: release claim: %w", err)
	}
	if released != 1 {
		return nil, fmt.Errorf("fail dispatch: exact resource claim changed concurrently: affected=%d", released)
	}
	if _, err := createFailureClassificationTx(ctx, q, FailureClassificationWrite{
		AttemptID: lifecycle.AttemptID, Category: execution.FailureStaleContext,
		Origin: "engine", Confidence: 1, EvidenceSource: DispatchFailureEvidenceSource,
		EvidenceReference: lifecycle.ID, Evidence: input.Reason,
	}, timestamp); err != nil {
		return nil, fmt.Errorf("fail dispatch: append recovery audit: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("fail dispatch: commit: %w", err)
	}
	return s.GetDispatchLifecycle(ctx, input.ID)
}

func (s *Store) failedDispatchRaceResult(
	ctx context.Context,
	input DispatchFailure,
	lifecycle DispatchLifecycle,
) (*DispatchLifecycle, error) {
	current, err := s.GetDispatchLifecycle(ctx, input.ID)
	if err != nil {
		return nil, fmt.Errorf("fail dispatch: reload concurrent lifecycle: %w", err)
	}
	if current.SessionID != input.SessionID || current.ResourceID != input.ResourceID ||
		current.HostSessionID != input.HostSessionID || current.WorkerID != input.WorkerID {
		return nil, fmt.Errorf("fail dispatch: lifecycle identity changed concurrently")
	}
	if _, err := s.queries.GetDispatchFailureAuditID(ctx, db.GetDispatchFailureAuditIDParams{
		AttemptID: lifecycle.AttemptID, DispatchID: lifecycle.ID,
	}); err == nil {
		return current, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("fail dispatch: inspect concurrent audit: %w", err)
	}
	if _, err := s.queries.GetExecutionManifestByAttempt(ctx, lifecycle.AttemptID); err == nil {
		return nil, fmt.Errorf("fail dispatch: execution already registered; dispatch_failed did not cancel it")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("fail dispatch: inspect concurrent execution: %w", err)
	}
	attempt, err := s.queries.GetGenerationAttempt(ctx, lifecycle.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("fail dispatch: inspect concurrent attempt: %w", err)
	}
	if attempt.Status == "abandoned" {
		return nil, fmt.Errorf("fail dispatch: corrective_action=rebuild_context; prepared attempt was abandoned concurrently")
	}
	return nil, fmt.Errorf("fail dispatch: exact lifecycle or resource claim changed concurrently")
}

// RecoverStalePreExecutionAttempts abandons active-session context attempts
// whose durable lifecycle has been inactive since before. Each attempt is
// recovered in its own transaction so a concurrent winner or malformed row
// cannot roll back successful recovery of an unrelated resource.
func (s *Store) RecoverStalePreExecutionAttempts(
	ctx context.Context,
	before time.Time,
) ([]PreparedContextRecovery, error) {
	if before.IsZero() {
		return nil, fmt.Errorf("recover stale pre-execution attempts: before is required")
	}
	cutoff := before.UTC()
	attemptIDs, err := s.queries.ListStalePreExecutionAttemptIDs(ctx, cutoff.Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("recover stale pre-execution attempts: list: %w", err)
	}
	reason := fmt.Sprintf(
		"engine recovered a stale prepared context with no execution manifest or candidate; lifecycle inactive before %s",
		cutoff.Format(time.RFC3339Nano),
	)
	recoveries := make([]PreparedContextRecovery, 0, len(attemptIDs))
	for _, attemptID := range attemptIDs {
		result, recoverErr := s.AbandonPreparedContext(ctx, PreparedContextAbandonment{
			AttemptID:      attemptID,
			Reason:         reason,
			EvidenceSource: PreExecutionRecoveryEvidenceSource,
			Before:         &cutoff,
		})
		if recoverErr != nil {
			return recoveries, fmt.Errorf(
				"recover stale pre-execution attempts: attempt %s: %w",
				attemptID,
				recoverErr,
			)
		}
		if result.Recovered {
			recoveries = append(recoveries, *result)
		}
	}
	return recoveries, nil
}

// AbandonPreparedContext is the sole pre-execution recovery transition. It
// atomically marks the immutable attempt history abandoned, appends an engine
// stale-context classification containing the recovery reason, and releases a
// session resource only when its job_id still equals this attempt. Prepared but
// unclaimed contexts leave the dispatchable session resource unchanged.
func (s *Store) AbandonPreparedContext(
	ctx context.Context,
	input PreparedContextAbandonment,
) (*PreparedContextRecovery, error) {
	if input.AttemptID == "" {
		return nil, fmt.Errorf("abandon prepared context: attempt_id is required")
	}
	if input.Reason == "" {
		return nil, fmt.Errorf("abandon prepared context: reason is required")
	}
	if input.EvidenceSource == "" {
		input.EvidenceSource = PreExecutionRecoveryEvidenceSource
	}
	result := &PreparedContextRecovery{AttemptID: input.AttemptID}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("abandon prepared context: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	q := s.queries.WithTx(tx)

	state, err := q.GetRecoverablePreExecutionAttempt(ctx, input.AttemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return nil, fmt.Errorf("abandon prepared context: inspect attempt: %w", err)
	}
	result.SessionID = state.SessionID
	result.ResourceID = state.ResourceID
	result.DispatchLifecycleID = state.DispatchLifecycleID
	if input.Before != nil {
		lastActivity, parseErr := time.Parse(time.RFC3339Nano, state.LastActivityAt)
		if parseErr != nil {
			return nil, fmt.Errorf("abandon prepared context: parse last lifecycle activity: %w", parseErr)
		}
		if !lastActivity.Before(input.Before.UTC()) {
			return result, nil
		}
	}

	changed, err := q.AbandonRecoverablePreExecutionAttempt(ctx, input.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("abandon prepared context: mark attempt abandoned: %w", err)
	}
	if changed == 0 {
		return result, nil
	}
	if changed != 1 {
		return nil, fmt.Errorf("abandon prepared context: expected one attempt, changed %d", changed)
	}

	claimed := state.ResourceJobID == input.AttemptID
	if claimed {
		released, releaseErr := q.ReleaseRecoveredPreExecutionClaim(ctx, db.ReleaseRecoveredPreExecutionClaimParams{
			LastError:  stringPtr(input.Reason),
			UpdatedAt:  now(),
			SessionID:  state.SessionID,
			ResourceID: state.ResourceID,
			JobID:      stringPtr(input.AttemptID),
		})
		if releaseErr != nil {
			return nil, fmt.Errorf("abandon prepared context: release claim: %w", releaseErr)
		}
		if released != 1 {
			return nil, fmt.Errorf(
				"abandon prepared context: matching claim changed concurrently: affected=%d",
				released,
			)
		}
		result.ClaimReleased = true
	}

	failure, err := createFailureClassificationTx(ctx, q, FailureClassificationWrite{
		AttemptID:         input.AttemptID,
		Category:          execution.FailureStaleContext,
		Origin:            "engine",
		Confidence:        1,
		EvidenceSource:    input.EvidenceSource,
		EvidenceReference: state.DispatchLifecycleID,
		Evidence:          input.Reason,
	}, now())
	if err != nil {
		return nil, fmt.Errorf("abandon prepared context: append recovery audit: %w", err)
	}
	result.FailureID = failure.ID
	result.Recovered = true
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("abandon prepared context: commit: %w", err)
	}
	return result, nil
}

// CountExecutionAttempts returns the total durable model-execution count for
// audit and metrics. Preparing, blocking, abandoning, or rebuilding context
// does not create a model execution. Executions invalidated by an engine-
// classified stale context are also exempt because they are framework refresh
// work, not model generations.
func (s *Store) CountExecutionAttempts(ctx context.Context, sessionID, resourceID string) (int, error) {
	count, err := s.queries.CountExecutionAttemptsBySessionResource(ctx, db.CountExecutionAttemptsBySessionResourceParams{
		InputSessionID:  sessionID,
		InputResourceID: resourceID,
	})
	if err != nil {
		return 0, fmt.Errorf("count execution attempts: %w", err)
	}
	return int(count), nil
}

// CountRepairEpochExecutionAttempts returns the retry-budget count since the
// resource's latest accepted immutable attempt. An accepted candidate closes
// one generation epoch; if affected-closure validation later reopens it, the
// resulting repair receives a fresh bounded budget without erasing the total
// execution history returned by CountExecutionAttempts.
func (s *Store) CountRepairEpochExecutionAttempts(ctx context.Context, sessionID, resourceID string) (int, error) {
	count, err := s.queries.CountRepairEpochExecutionAttemptsBySessionResource(
		ctx,
		db.CountRepairEpochExecutionAttemptsBySessionResourceParams{
			InputSessionID: sessionID, InputResourceID: resourceID,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("count repair-epoch execution attempts: %w", err)
	}
	return int(count), nil
}

// CountAffectedClosureRepairCycles returns the durable number of real source-
// repair cycles charged to one resource in an active session. An unresolved
// affected-closure integration failure is the currently available repair; a
// failure resolved by an accepted descendant remains part of the audit count.
// Human/triage resolutions without a replacement attempt and legacy invalid-
// validation classifications are deliberately excluded.
func (s *Store) CountAffectedClosureRepairCycles(ctx context.Context, sessionID, resourceID string) (int, error) {
	count, err := s.queries.CountAffectedClosureRepairCyclesBySessionResource(
		ctx,
		db.CountAffectedClosureRepairCyclesBySessionResourceParams{
			InputSessionID: sessionID, InputResourceID: resourceID,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("count affected-closure repair cycles: %w", err)
	}
	return int(count), nil
}

// MaxAffectedClosureRepairCycles returns the largest per-resource closure-
// repair count in a session. Wave status uses the maximum because one closure
// observation may implicate owners with different prior repair histories.
func (s *Store) MaxAffectedClosureRepairCycles(ctx context.Context, sessionID string) (int, error) {
	count, err := s.queries.MaxAffectedClosureRepairCyclesBySession(ctx, sessionID)
	if err != nil {
		return 0, fmt.Errorf("max affected-closure repair cycles: %w", err)
	}
	return int(count), nil
}
