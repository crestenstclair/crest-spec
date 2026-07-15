package store

import (
	"context"
	"testing"
	"time"

	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	"github.com/stretchr/testify/require"
)

func TestContextManifestRoundTripDeduplicatesContentAndLinksRetries(t *testing.T) {
	st := newContextManifestStore(t, "resource.synth")
	ctx := context.Background()

	first, err := st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-1", "manifest-1", "resource.synth"),
		Dispatch: true,
	})
	require.NoError(t, err)
	require.Equal(t, 1, first.Attempt.RetryNumber)
	require.Empty(t, first.Attempt.ParentAttemptID)

	resourceState, err := st.GetSessionResource("session-1", "resource.synth")
	require.NoError(t, err)
	require.Equal(t, "dispatched", resourceState.State)
	require.Equal(t, "queued", resourceState.Phase)

	secondInput := testContextManifest("attempt-2", "manifest-2", "resource.synth")
	second, err := st.CreateContextManifest(ctx, ContextManifestWrite{Manifest: secondInput, Dispatch: true})
	require.NoError(t, err)
	require.Equal(t, 2, second.Attempt.RetryNumber)
	require.Equal(t, "attempt-1", second.Attempt.ParentAttemptID)

	// System, rendered, and section content intentionally match. The same
	// snapshot is also reused on retry, so only one SQLite blob is necessary.
	blobCount, err := st.CountContentBlobs(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), blobCount)

	got, err := st.GetContextManifestByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Equal(t, "manifest-1", got.ID)
	require.Equal(t, "shared immutable context", got.SystemPrompt)
	require.Equal(t, "shared immutable context", got.RenderedPrompt)
	require.Equal(t, map[string]string{"resource": "template-v1"}, got.TemplateHashes)
	require.Len(t, got.Sections, 2)
	require.Equal(t, "shared immutable context", got.Sections[0].Content)
	require.Empty(t, got.Sections[1].Content)
	require.Equal(t, "not relevant to this resource", got.Sections[1].Reason)

	attempts, err := st.ListGenerationAttemptsBySession(ctx, "session-1")
	require.NoError(t, err)
	require.Len(t, attempts, 2)
	require.Equal(t, "context_prepared", attempts[0].Status)
}

func TestContextManifestBlockedAttemptDoesNotDispatch(t *testing.T) {
	st := newContextManifestStore(t, "resource.blocked")
	manifest := testContextManifest("attempt-blocked", "manifest-blocked", "resource.blocked")
	manifest.Blocked = true
	manifest.BlockedReason = "mandatory context exceeds budget"
	manifest.Sections[0].Decision = string(contextmanifest.Omitted)
	manifest.Sections[0].Reason = manifest.BlockedReason
	manifest.Sections[0].Content = ""
	manifest.Sections[0].SelectedBytes = 0
	manifest.Sections[0].EstimatedTokens = 0
	manifest.EstimatedTokens = 0
	manifest.SelectedBytes = 0

	got, err := st.CreateContextManifest(context.Background(), ContextManifestWrite{Manifest: manifest, Dispatch: false})
	require.NoError(t, err)
	require.Equal(t, "context_blocked", got.Attempt.Status)

	resourceState, err := st.GetSessionResource("session-1", "resource.blocked")
	require.NoError(t, err)
	require.Equal(t, "pending", resourceState.State)
}

func TestContextManifestCreationIsAtomic(t *testing.T) {
	st := newContextManifestStore(t, "resource.atomic")
	manifest := testContextManifest("attempt-atomic", "manifest-atomic", "resource.atomic")
	duplicate := manifest.Sections[0]
	duplicate.ID = "section-duplicate"
	manifest.Sections = append(manifest.Sections, duplicate)

	_, err := st.CreateContextManifest(context.Background(), ContextManifestWrite{Manifest: manifest, Dispatch: true})
	require.ErrorContains(t, err, "UNIQUE constraint failed: context_sections.manifest_id, context_sections.ordinal")

	attempts, err := st.ListGenerationAttemptsBySession(context.Background(), "session-1")
	require.NoError(t, err)
	require.Empty(t, attempts)
	resourceState, err := st.GetSessionResource("session-1", "resource.atomic")
	require.NoError(t, err)
	require.Equal(t, "pending", resourceState.State)
	blobCount, err := st.CountContentBlobs(context.Background())
	require.NoError(t, err)
	require.Zero(t, blobCount)
}

func TestVacuumReleasesUnreferencedContextSnapshots(t *testing.T) {
	st := newContextManifestStore(t, "resource.vacuum")
	_, err := st.CreateContextManifest(context.Background(), ContextManifestWrite{
		Manifest: testContextManifest("attempt-vacuum", "manifest-vacuum", "resource.vacuum"), Dispatch: true,
	})
	require.NoError(t, err)

	deleted, err := st.Vacuum(time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.GreaterOrEqual(t, deleted, 4)
	_, err = st.GetContextManifest(context.Background(), "manifest-vacuum")
	require.Error(t, err)
	blobCount, err := st.CountContentBlobs(context.Background())
	require.NoError(t, err)
	require.Zero(t, blobCount)
}

func TestVacuumRetainsOldAttemptThatIsAncestorOfRetainedRetry(t *testing.T) {
	st := newContextManifestStore(t, "resource.ancestry")
	ctx := context.Background()
	_, err := st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-old", "manifest-old", "resource.ancestry"), Dispatch: true,
	})
	require.NoError(t, err)
	_, err = st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: testContextManifest("attempt-future", "manifest-future", "resource.ancestry"), Dispatch: true,
	})
	require.NoError(t, err)
	_, err = st.sqlDB.Exec("UPDATE generation_attempts SET created_at = ? WHERE id = ?", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), "attempt-future")
	require.NoError(t, err)

	_, err = st.Vacuum(time.Now())
	require.NoError(t, err)
	oldManifest, err := st.GetContextManifest(ctx, "manifest-old")
	require.NoError(t, err)
	require.Equal(t, "attempt-old", oldManifest.Attempt.ID)
	futureManifest, err := st.GetContextManifest(ctx, "manifest-future")
	require.NoError(t, err)
	require.Equal(t, "attempt-old", futureManifest.Attempt.ParentAttemptID)
}

func newContextManifestStore(t *testing.T, resourceID string) *Store {
	t.Helper()
	st, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	require.NoError(t, st.CreateApply("apply-1", "spec-hash"))
	require.NoError(t, st.CreateSession(Session{ID: "session-1", ApplyID: "apply-1", PlanJSON: "{}", WavesJSON: "[]", HashesJSON: "{}"}))
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: "session-1", ResourceID: resourceID, State: "pending", MaxRetries: 3,
	}))
	return st
}

func testContextManifest(attemptID, manifestID, resourceID string) ContextManifest {
	content := "shared immutable context"
	return ContextManifest{
		ID: manifestID,
		Attempt: GenerationAttempt{
			ID: attemptID, SessionID: "session-1", ApplyID: "apply-1", ResourceID: resourceID,
			PlanOperationID: "operation-1", Role: "resource_implementer", RolePolicyVersion: "role-policy-v1",
		},
		SelectorVersion: contextmanifest.SelectorVersion, EstimatorVersion: contextmanifest.EstimatorVersion,
		SelectionStrategy: "goal-directed-priority", BudgetTokens: 4096,
		EstimatedTokens: contextmanifest.EstimateTokens(content), OriginalBytes: len(content), SelectedBytes: len(content),
		TemplateHashes: map[string]string{"resource": "template-v1"},
		SystemPrompt:   content, RenderedPrompt: content, ContextHash: contextmanifest.Hash("manifest-context"),
		Sections: []ContextManifestSection{
			{
				ID: attemptID + "-section-included", Ordinal: 0, Kind: "resource_contract", Title: "Resource contract",
				SourceKind: "specification", SourceID: resourceID, Priority: int(contextmanifest.PriorityContract), Mandatory: true,
				Decision: string(contextmanifest.Included), Reason: "required implementation contract",
				OriginalHash: contextmanifest.Hash(content), OriginalBytes: len(content), SelectedBytes: len(content),
				EstimatedTokens: contextmanifest.EstimateTokens(content), Content: content,
			},
			{
				ID: attemptID + "-section-omitted", Ordinal: 1, Kind: "background", Title: "Background",
				SourceKind: "repository", SourceID: "readme", Priority: int(contextmanifest.PriorityBackground),
				Decision: string(contextmanifest.Omitted), Reason: "not relevant to this resource",
				OriginalHash: contextmanifest.Hash("unused"), OriginalBytes: len("unused"),
			},
		},
	}
}
