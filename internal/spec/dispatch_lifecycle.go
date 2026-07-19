package spec

import (
	"context"
	"fmt"

	"github.com/crestenstclair/crest-spec/internal/store"
)

type DispatchEventOptions struct {
	Event         string
	DispatchID    string
	SessionID     string
	ResourceID    string
	HostSessionID string
	WorkerID      string
	Reason        string
}

// ReportDispatchEvent records host-observed lifecycle boundaries. Server-owned
// boundaries (context preparation, execution registration, and candidate
// disposition) are recorded automatically and cannot be supplied here.
func (s *Spec) ReportDispatchEvent(ctx context.Context, opts DispatchEventOptions) (*store.DispatchLifecycle, error) {
	if opts.SessionID == "" || opts.ResourceID == "" {
		return nil, fmt.Errorf("dispatch event: session_id and resource_id are required")
	}
	session, err := s.store.GetSession(opts.SessionID)
	if err != nil {
		return nil, fmt.Errorf("dispatch event: session: %w", err)
	}
	if session.Status != "active" {
		return nil, fmt.Errorf("dispatch event: session %s is %s", opts.SessionID, session.Status)
	}
	if _, err := s.store.GetSessionResource(opts.SessionID, opts.ResourceID); err != nil {
		return nil, fmt.Errorf("dispatch event: session resource: %w", err)
	}
	input := store.DispatchRequest{
		ID: opts.DispatchID, SessionID: opts.SessionID, ResourceID: opts.ResourceID,
		HostSessionID: opts.HostSessionID, WorkerID: opts.WorkerID,
	}
	switch opts.Event {
	case "dispatch_requested":
		if opts.DispatchID == "" {
			return nil, fmt.Errorf("dispatch event: dispatch_id from a prepared context is required")
		}
		lifecycle, lifecycleErr := s.store.GetDispatchLifecycle(ctx, opts.DispatchID)
		if lifecycleErr != nil || lifecycle.SessionID != opts.SessionID || lifecycle.ResourceID != opts.ResourceID || lifecycle.AttemptID == "" {
			return nil, fmt.Errorf("dispatch event: dispatch_id is not bound to a prepared context for this session resource")
		}
		if lifecycle.DispatchRequestedAt != nil {
			return s.store.RequestDispatch(ctx, input)
		}
		manifest, manifestErr := s.store.GetContextManifestByAttempt(ctx, lifecycle.AttemptID)
		if manifestErr != nil {
			return nil, fmt.Errorf("dispatch event: prepared context: %w", manifestErr)
		}
		if sourceErr := s.validateSelectedRepositorySources(manifest); sourceErr != nil {
			reason := fmt.Sprintf("prepared context became stale before dispatch: %v", sourceErr)
			recovered, recoverErr := s.store.AbandonPreparedContext(ctx, store.PreparedContextAbandonment{
				AttemptID: manifest.Attempt.ID, Reason: reason,
				EvidenceSource: "dispatch_source_validation",
			})
			if recoverErr != nil {
				return nil, fmt.Errorf("dispatch event: rebuild_context: invalidate stale context: %w", recoverErr)
			}
			if !recovered.Recovered {
				return nil, fmt.Errorf("dispatch event: rebuild_context: stale context changed concurrently")
			}
			return nil, fmt.Errorf("dispatch event: rebuild_context: %s", reason)
		}
		return s.store.RequestDispatch(ctx, input)
	case "worker_acknowledged":
		return s.store.AcknowledgeDispatch(ctx, input)
	case "dispatch_failed":
		return s.store.FailDispatch(ctx, store.DispatchFailure{
			ID: opts.DispatchID, SessionID: opts.SessionID, ResourceID: opts.ResourceID,
			HostSessionID: opts.HostSessionID, WorkerID: opts.WorkerID, Reason: opts.Reason,
		})
	default:
		return nil, fmt.Errorf("dispatch event: event must be dispatch_requested, worker_acknowledged, or dispatch_failed")
	}
}
