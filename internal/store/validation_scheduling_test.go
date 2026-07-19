package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReopenSessionResourcesAtomicallyRequeuesMatchedCommittedOwners(t *testing.T) {
	st := newContextManifestStore(t, "resource.provider")
	require.NoError(t, st.UpdateSessionResourceState(
		"session-1", "resource.provider", "committed", "", "", 2, "attempt-provider",
	))
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: "resource.consumer", State: "committed", Attempts: 1, MaxRetries: 3,
	}))
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: "resource.skipped", State: "skipped", LastError: "waived", MaxRetries: 3,
	}))

	reopened, err := st.ReopenSessionResources(context.Background(), "session-1", []string{
		"resource.consumer", "resource.provider", "resource.consumer", "resource.skipped",
	}, "compiler diagnostics mention both owners")
	require.NoError(t, err)
	require.Equal(t, 2, reopened)

	for _, resourceID := range []string{"resource.provider", "resource.consumer"} {
		resource, getErr := st.GetSessionResource("session-1", resourceID)
		require.NoError(t, getErr)
		require.Equal(t, "pending", resource.State)
		require.Equal(t, "integration_failed", resource.Phase)
		require.Empty(t, resource.JobID)
		require.Zero(t, resource.Attempts, "reopen must start a fresh repair retry epoch")
		require.Equal(t, "compiler diagnostics mention both owners", resource.LastError)
	}
	skipped, err := st.GetSessionResource("session-1", "resource.skipped")
	require.NoError(t, err)
	require.Equal(t, "skipped", skipped.State)
	require.Equal(t, "waived", skipped.LastError)
}
