package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/db"
)

// DispatchLifecycle is the durable, server-observed hand-off timeline for one
// resource attempt. Zero timestamps mean the event was not observed; callers
// must not infer them from adjacent events.
type DispatchLifecycle struct {
	ID                    string     `json:"dispatch_id"`
	SessionID             string     `json:"session_id"`
	ResourceID            string     `json:"resource_id"`
	AttemptID             string     `json:"attempt_id,omitempty"`
	HostSessionID         string     `json:"host_session_id,omitempty"`
	WorkerID              string     `json:"worker_id,omitempty"`
	DispatchRequestedAt   *time.Time `json:"dispatch_requested_at,omitempty"`
	WorkerAcknowledgedAt  *time.Time `json:"worker_acknowledged_at,omitempty"`
	ContextRequestedAt    *time.Time `json:"context_requested_at,omitempty"`
	ContextPreparedAt     *time.Time `json:"context_prepared_at,omitempty"`
	ExecutionRegisteredAt *time.Time `json:"execution_registered_at,omitempty"`
	CandidateSubmittedAt  *time.Time `json:"candidate_submitted_at,omitempty"`
	CandidateAcceptedAt   *time.Time `json:"candidate_accepted_at,omitempty"`
	CandidateRejectedAt   *time.Time `json:"candidate_rejected_at,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
}

type DispatchRequest struct {
	ID            string
	SessionID     string
	ResourceID    string
	HostSessionID string
	WorkerID      string
}

// RequestDispatch atomically records a real worker assignment and claims the
// session resource by the already-prepared attempt ID. A dispatch lifecycle is
// created by RequestContext and bound while its context manifest is persisted;
// accepting arbitrary lifecycle IDs here would recreate the old state in which
// a resource could be called dispatched without an executable attempt.
func (s *Store) RequestDispatch(ctx context.Context, input DispatchRequest) (_ *DispatchLifecycle, err error) {
	if input.SessionID == "" || input.ResourceID == "" {
		return nil, fmt.Errorf("request dispatch: session_id and resource_id are required")
	}
	input.HostSessionID = strings.TrimSpace(input.HostSessionID)
	input.WorkerID = strings.TrimSpace(input.WorkerID)
	if input.HostSessionID == "" || input.WorkerID == "" {
		return nil, fmt.Errorf("request dispatch: host_session_id and worker_id are required for explicit dispatch")
	}
	if input.ID == "" {
		return nil, fmt.Errorf("request dispatch: dispatch_id from a prepared context is required")
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("request dispatch: begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	q := s.queries.WithTx(tx)
	row, err := q.GetDispatchLifecycle(ctx, input.ID)
	if err != nil {
		return nil, fmt.Errorf("request dispatch: prepared lifecycle: %w", mapNotFound(err))
	}
	lifecycle := dbDispatchLifecycle(row)
	if lifecycle.SessionID != input.SessionID || lifecycle.ResourceID != input.ResourceID {
		return nil, fmt.Errorf("request dispatch: dispatch_id %s belongs to another session resource", input.ID)
	}
	if identityErr := validateDispatchIdentity(lifecycle, input, "request dispatch"); identityErr != nil {
		return nil, identityErr
	}
	if lifecycle.AttemptID == "" || lifecycle.ContextPreparedAt == nil {
		return nil, fmt.Errorf("request dispatch: dispatch_id %s is not bound to a prepared context attempt", input.ID)
	}
	attempt, err := q.GetGenerationAttempt(ctx, lifecycle.AttemptID)
	if err != nil {
		return nil, fmt.Errorf("request dispatch: prepared attempt: %w", mapNotFound(err))
	}
	resource, err := q.GetSessionResource(ctx, db.GetSessionResourceParams{
		SessionID: input.SessionID, ResourceID: input.ResourceID,
	})
	if err != nil {
		return nil, fmt.Errorf("request dispatch: session resource: %w", mapNotFound(err))
	}
	if lifecycle.DispatchRequestedAt != nil && !dispatchIdentityNeedsFill(lifecycle, input) {
		switch attempt.Status {
		case "context_prepared", "candidate_submitted":
			if resource.State != "dispatched" || stringVal(resource.JobID) != lifecycle.AttemptID {
				return nil, fmt.Errorf("request dispatch: rebuild_context: active attempt no longer owns the resource claim")
			}
		case "abandoned":
			return nil, fmt.Errorf("request dispatch: rebuild_context: prepared attempt was abandoned")
		case "accepted", "rejected":
			// The assignment is terminal. Reporting the already-recorded event is
			// still idempotent, but it never reacquires the resource.
		default:
			return nil, fmt.Errorf("request dispatch: attempt %s has unsupported replay status %s", attempt.ID, attempt.Status)
		}
		if err = tx.Commit(); err != nil {
			return nil, fmt.Errorf("request dispatch: commit replay: %w", err)
		}
		return &lifecycle, nil
	}
	if attempt.Status != "context_prepared" {
		return nil, fmt.Errorf("request dispatch: attempt %s is %s, not context_prepared", attempt.ID, attempt.Status)
	}

	timestamp := now()
	changed, err := q.RequestExistingDispatchLifecycle(ctx, db.RequestExistingDispatchLifecycleParams{
		DispatchRequestedAt: timestamp, HostSessionID: input.HostSessionID, WorkerID: input.WorkerID,
		UpdatedAt: timestamp, ID: input.ID, SessionID: input.SessionID, ResourceID: input.ResourceID,
	})
	if err != nil {
		return nil, fmt.Errorf("request dispatch: record assignment: %w", err)
	}
	if changed == 0 {
		return nil, fmt.Errorf(
			"request dispatch: worker %s already has a nonterminal assignment in host session %s",
			input.WorkerID, input.HostSessionID,
		)
	}
	claimed, err := q.SetSessionResourceDispatchedTx(ctx, db.SetSessionResourceDispatchedTxParams{
		JobID: &lifecycle.AttemptID, DispatchedAt: timestamp, UpdatedAt: timestamp,
		SessionID: input.SessionID, ResourceID: input.ResourceID,
	})
	if err != nil {
		return nil, fmt.Errorf("request dispatch: claim prepared attempt: %w", err)
	}
	if claimed != 1 {
		return nil, fmt.Errorf("request dispatch: resource is already claimed or terminal")
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("request dispatch: commit: %w", err)
	}
	return s.GetDispatchLifecycle(ctx, input.ID)
}

// AcknowledgeDispatch records when the assigned worker is running. The final
// SQL transition rechecks exact claim ownership so recovery and acknowledgement
// cannot both win after the preliminary read checks.
func (s *Store) AcknowledgeDispatch(ctx context.Context, input DispatchRequest) (*DispatchLifecycle, error) {
	if input.ID == "" || input.SessionID == "" || input.ResourceID == "" {
		return nil, fmt.Errorf("acknowledge dispatch: dispatch_id, session_id, and resource_id are required")
	}
	input.HostSessionID = strings.TrimSpace(input.HostSessionID)
	input.WorkerID = strings.TrimSpace(input.WorkerID)
	if input.HostSessionID == "" || input.WorkerID == "" {
		return nil, fmt.Errorf("acknowledge dispatch: host_session_id and worker_id are required")
	}
	existing, err := s.GetDispatchLifecycle(ctx, input.ID)
	if err != nil || existing.SessionID != input.SessionID || existing.ResourceID != input.ResourceID {
		return nil, fmt.Errorf("acknowledge dispatch: dispatch lifecycle not found for session resource")
	}
	if err := validateDispatchIdentity(*existing, input, "acknowledge dispatch"); err != nil {
		return nil, err
	}
	if existing.DispatchRequestedAt == nil {
		return nil, fmt.Errorf("acknowledge dispatch: dispatch_requested must be recorded first")
	}
	if existing.WorkerAcknowledgedAt != nil && !dispatchIdentityNeedsFill(*existing, input) {
		return existing, nil
	}
	if existing.AttemptID == "" {
		return nil, fmt.Errorf("acknowledge dispatch: lifecycle is not bound to a prepared attempt")
	}
	resource, err := s.GetSessionResource(input.SessionID, input.ResourceID)
	if err != nil {
		return nil, fmt.Errorf("acknowledge dispatch: session resource: %w", err)
	}
	if resource.State != "dispatched" || resource.JobID != existing.AttemptID {
		return nil, fmt.Errorf("acknowledge dispatch: prepared attempt no longer owns the resource claim")
	}
	timestamp := now()
	changed, err := s.queries.AcknowledgeDispatchLifecycle(ctx, db.AcknowledgeDispatchLifecycleParams{
		AcknowledgedAt: timestamp, HostSessionID: input.HostSessionID, WorkerID: input.WorkerID,
		UpdatedAt: timestamp, ID: input.ID, SessionID: input.SessionID, ResourceID: input.ResourceID,
	})
	if err != nil {
		return nil, fmt.Errorf("acknowledge dispatch: update: %w", err)
	}
	if changed == 0 {
		current, getErr := s.GetDispatchLifecycle(ctx, input.ID)
		if getErr != nil {
			return nil, fmt.Errorf("acknowledge dispatch: reload concurrent lifecycle: %w", getErr)
		}
		if identityErr := validateDispatchIdentity(*current, input, "acknowledge dispatch"); identityErr != nil {
			return nil, identityErr
		}
		if current.WorkerAcknowledgedAt != nil && !dispatchIdentityNeedsFill(*current, input) {
			return current, nil
		}
		return nil, fmt.Errorf("acknowledge dispatch: lifecycle changed concurrently")
	}
	return s.GetDispatchLifecycle(ctx, input.ID)
}

func validateDispatchIdentity(existing DispatchLifecycle, input DispatchRequest, operation string) error {
	if existing.HostSessionID != "" && input.HostSessionID != "" && existing.HostSessionID != input.HostSessionID {
		return fmt.Errorf("%s: host_session_id conflicts with the first recorded identity", operation)
	}
	if existing.WorkerID != "" && input.WorkerID != "" && existing.WorkerID != input.WorkerID {
		return fmt.Errorf("%s: worker_id conflicts with the first recorded identity", operation)
	}
	return nil
}

func dispatchIdentityNeedsFill(existing DispatchLifecycle, input DispatchRequest) bool {
	return (existing.HostSessionID == "" && input.HostSessionID != "") ||
		(existing.WorkerID == "" && input.WorkerID != "")
}

// RequestContext starts the engine-owned context preparation interval. A new
// lifecycle has no host assignment; context preparation binds its immutable
// attempt before the host may record dispatch_requested.
func (s *Store) RequestContext(ctx context.Context, input DispatchRequest) (*DispatchLifecycle, error) {
	if input.SessionID == "" || input.ResourceID == "" {
		return nil, fmt.Errorf("request context: session_id and resource_id are required")
	}
	timestamp := now()
	if input.ID == "" {
		input.ID = uuid.NewString()
		if err := s.queries.CreateDispatchLifecycle(ctx, db.CreateDispatchLifecycleParams{
			ID: input.ID, SessionID: input.SessionID, ResourceID: input.ResourceID,
			HostSessionID: input.HostSessionID, WorkerID: input.WorkerID,
			ContextRequestedAt: timestamp, CreatedAt: timestamp, UpdatedAt: timestamp,
		}); err != nil {
			return nil, fmt.Errorf("request context: create implicit lifecycle: %w", err)
		}
		return s.GetDispatchLifecycle(ctx, input.ID)
	}
	changed, err := s.queries.RequestDispatchContext(ctx, db.RequestDispatchContextParams{
		ContextRequestedAt: timestamp, UpdatedAt: timestamp, ID: input.ID,
		SessionID: input.SessionID, ResourceID: input.ResourceID,
	})
	if err != nil {
		return nil, fmt.Errorf("request context: update: %w", err)
	}
	if changed == 0 {
		return nil, fmt.Errorf("request context: dispatch lifecycle not found, mismatched, or already bound")
	}
	return s.GetDispatchLifecycle(ctx, input.ID)
}

func (s *Store) GetDispatchLifecycle(ctx context.Context, id string) (*DispatchLifecycle, error) {
	row, err := s.queries.GetDispatchLifecycle(ctx, id)
	if err != nil {
		return nil, mapNotFound(err)
	}
	value := dbDispatchLifecycle(row)
	return &value, nil
}

func (s *Store) ListDispatchLifecyclesBySession(ctx context.Context, sessionID string) ([]DispatchLifecycle, error) {
	rows, err := s.queries.ListDispatchLifecyclesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	result := make([]DispatchLifecycle, 0, len(rows))
	for _, row := range rows {
		result = append(result, dbDispatchLifecycle(row))
	}
	return result, nil
}

func dbDispatchLifecycle(row db.DispatchLifecycle) DispatchLifecycle {
	value := DispatchLifecycle{
		ID: row.ID, SessionID: row.SessionID, ResourceID: row.ResourceID,
		HostSessionID: row.HostSessionID, WorkerID: row.WorkerID,
		CreatedAt: parseTime(row.CreatedAt), UpdatedAt: parseTime(row.UpdatedAt),
	}
	if row.AttemptID != nil {
		value.AttemptID = *row.AttemptID
	}
	value.DispatchRequestedAt = optionalStoreTime(row.DispatchRequestedAt)
	value.WorkerAcknowledgedAt = optionalStoreTime(row.WorkerAcknowledgedAt)
	value.ContextRequestedAt = optionalStoreTime(row.ContextRequestedAt)
	value.ContextPreparedAt = optionalStoreTime(row.ContextPreparedAt)
	value.ExecutionRegisteredAt = optionalStoreTime(row.ExecutionRegisteredAt)
	value.CandidateSubmittedAt = optionalStoreTime(row.CandidateSubmittedAt)
	value.CandidateAcceptedAt = optionalStoreTime(row.CandidateAcceptedAt)
	value.CandidateRejectedAt = optionalStoreTime(row.CandidateRejectedAt)
	return value
}

func optionalStoreTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed := parseTime(value)
	if parsed.IsZero() {
		return nil
	}
	return &parsed
}
