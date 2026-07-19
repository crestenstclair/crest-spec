package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestVacuumRetainsWorkspaceJournalBlobsAndCompletes(t *testing.T) {
	ctx := context.Background()
	st := newContextManifestStore(t, "resource.synth")
	prepareAndClaimTestContext(
		t, st, testContextManifest("attempt-1", "manifest-1", "resource.synth"),
	)
	started, err := st.StartExecution(ctx, testExecutionManifest())
	require.NoError(t, err)

	original := []byte("exact pre-commit bytes available only through the journal\n")
	candidateBytes := []byte("candidate bytes\n")
	journal, err := st.PrepareWorkspaceCommitJournal(ctx, WorkspaceCommitJournalInput{
		ExecutionID: started.ID,
		ProjectRoot: "/tmp/crest-spec-vacuum-journal",
		Files: []WorkspaceCommitJournalFile{{
			Path: "src/synth.go", OriginalExists: true,
			OriginalContent: original, OriginalMode: 0o640,
			CandidateContent: candidateBytes, CandidateContentHash: hashWorkspaceBytes(candidateBytes),
			CandidateMode: 0o640, WriteIntent: "modify", Ordinal: 0,
		}},
	})
	require.NoError(t, err)
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: "src/synth.go", Content: string(candidateBytes), WriteIntent: "modify",
	}})
	require.NoError(t, err)
	require.NoError(t, st.BindWorkspaceCommitCandidate(ctx, journal.ID, candidate.ID))
	require.NoError(t, st.MarkWorkspaceCommitApplied(ctx, journal.ID))
	require.NoError(t, st.ResolveWorkspaceCommitJournal(
		ctx, journal.ID, WorkspaceCommitRolledBack, "test recovery completed",
	))

	_, err = st.Vacuum(time.Now().Add(24 * time.Hour))
	require.NoError(t, err, "vacuum must not delete blobs referenced by terminal workspace journals")
	persisted, err := st.GetWorkspaceCommitJournal(ctx, journal.ID)
	require.NoError(t, err)
	require.Equal(t, WorkspaceCommitRolledBack, persisted.Status)
	require.Len(t, persisted.Files, 1)
	require.Equal(t, original, persisted.Files[0].OriginalContent)
	require.Equal(t, candidateBytes, persisted.Files[0].CandidateContent)
	require.Equal(t, uint32(0o640), persisted.Files[0].OriginalMode)
	require.Equal(t, uint32(0o640), persisted.Files[0].CandidateMode)
}
