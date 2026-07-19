package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// prepareAndClaimTestContext follows the production lifecycle contract for
// store tests that need an executable context: prepare immutable context,
// record a real worker assignment, then acknowledge it. Tests concerned only
// with context persistence should call CreateContextManifest directly.
func prepareAndClaimTestContext(
	t *testing.T,
	st *Store,
	manifest ContextManifest,
) *DispatchLifecycle {
	t.Helper()
	ctx := context.Background()
	lifecycle, err := st.RequestContext(ctx, DispatchRequest{
		SessionID:  manifest.Attempt.SessionID,
		ResourceID: manifest.Attempt.ResourceID,
	})
	require.NoError(t, err)
	_, err = st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest:            manifest,
		DispatchLifecycleID: lifecycle.ID,
	})
	require.NoError(t, err)
	lifecycle, err = st.RequestDispatch(ctx, DispatchRequest{
		ID: lifecycle.ID, SessionID: manifest.Attempt.SessionID,
		ResourceID:    manifest.Attempt.ResourceID,
		HostSessionID: "host-session-1", WorkerID: "worker-" + manifest.Attempt.ID,
	})
	require.NoError(t, err)
	lifecycle, err = st.AcknowledgeDispatch(ctx, DispatchRequest{
		ID: lifecycle.ID, SessionID: manifest.Attempt.SessionID,
		ResourceID:    manifest.Attempt.ResourceID,
		HostSessionID: "host-session-1", WorkerID: "worker-" + manifest.Attempt.ID,
	})
	require.NoError(t, err)
	return lifecycle
}
