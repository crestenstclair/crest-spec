package spec

import (
	"context"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/require"
)

func dispatchAndAcknowledgePreparedContext(
	t *testing.T,
	s *Spec,
	ctx context.Context,
	prepared *ContextResult,
	sessionID string,
	resourceID string,
	hostSessionID string,
	workerID string,
) *store.DispatchLifecycle {
	t.Helper()
	if hostSessionID == "" {
		hostSessionID = "spec-test-host-session"
	}
	if workerID == "" {
		workerID = "worker-" + prepared.AttemptID
	}
	lifecycle, err := s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "dispatch_requested", DispatchID: prepared.DispatchID,
		SessionID: sessionID, ResourceID: resourceID,
		HostSessionID: hostSessionID, WorkerID: workerID,
	})
	require.NoError(t, err)
	lifecycle, err = s.ReportDispatchEvent(ctx, DispatchEventOptions{
		Event: "worker_acknowledged", DispatchID: prepared.DispatchID,
		SessionID: sessionID, ResourceID: resourceID,
		HostSessionID: hostSessionID, WorkerID: workerID,
	})
	require.NoError(t, err)
	return lifecycle
}
