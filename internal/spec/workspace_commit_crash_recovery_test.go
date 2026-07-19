package spec

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

var errSimulatedCommitProcessCrash = &simulatedCommitProcessCrash{}

type simulatedCommitProcessCrash struct{}

func (*simulatedCommitProcessCrash) Error() string {
	return "simulated process crash during workspace commit"
}

// crashAfterFirstWorkspaceWriteFS models process death at the filesystem
// boundary: the first WriteFile reaches the real workspace, then control never
// returns to CommitAttempt. In particular, none of its ordinary error cleanup
// runs before the persistent store is closed and reopened.
type crashAfterFirstWorkspaceWriteFS struct {
	OSFileSystem
	writes     int
	crashAfter int
}

func (f *crashAfterFirstWorkspaceWriteFS) WriteFileAtomic(path string, data []byte, perm fs.FileMode) error {
	if err := f.OSFileSystem.WriteFileAtomic(path, data, perm); err != nil {
		return err
	}
	f.writes++
	if f.writes == f.crashAfter {
		panic(errSimulatedCommitProcessCrash)
	}
	return nil
}

// TestWorkspaceCommitCrashRestoresOriginalTreeAfterRestart exercises the real
// persistent recovery path around the gap between candidate submission and
// FinalizeCandidate. The candidate changes one existing file and creates one
// new file; the host dies after exactly one of those writes reaches disk.
func TestWorkspaceCommitCrashRestoresOriginalTreeAfterRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	stateDir := filepath.Join(root, ".crest-spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"),
		[]byte(withTestIntent(commitTestSpecCUE)),
		0o644,
	))

	const originalExisting = "package src\n\nconst Value = \"original\\x00bytes\"\n"
	const candidateExisting = "package src\n\nconst Value = \"candidate\"\n"
	const candidateNew = "package src\n\nconst Created = true\n"
	existingPath := filepath.Join(root, "src", "first.go")
	newPath := filepath.Join(root, "src", "second.go")

	statePath := filepath.Join(stateDir, "state.db")
	open := func(files fileSystem) (*Spec, *store.Store) {
		t.Helper()
		st, err := store.New(statePath)
		require.NoError(t, err)
		return New(st, files, &config.Config{
			SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3,
		}), st
	}

	initial, initialStore := open(OSFileSystem{})
	begin, err := initial.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.Len(t, begin.Plan, 1)
	resourceID := begin.Plan[0].ResourceID
	baseline, err := commitTestAttempt(t, initial, ctx, begin.SessionID, resourceID, []CommitFile{{
		Path: "src/first.go", Content: originalExisting,
	}}, "", "test-model")
	require.NoError(t, err)
	require.True(t, baseline.Committed)
	require.NoError(t, os.Chmod(existingPath, 0o640))
	_, err = initialStore.ReopenSessionResources(
		ctx, begin.SessionID, []string{resourceID}, "exercise interrupted minimal-diff commit",
	)
	require.NoError(t, err)
	prepared, err := initial.PrepareContext(ctx, ContextOptions{
		SessionID: begin.SessionID, ResourceID: resourceID,
	})
	require.NoError(t, err)
	lifecycle := dispatchAndAcknowledgePreparedContext(
		t, initial, ctx, prepared, begin.SessionID, resourceID, "crashing-host", "crashing-worker",
	)
	started, err := initial.StartExecution(ctx, ExecutionStartOptions{
		AttemptID: prepared.AttemptID, ProtocolVersion: execution.ProtocolVersion,
		IdempotencyKey: uuid.NewString(), ContextHash: prepared.ContextHash,
		HostName: "crash-test", HostSessionID: lifecycle.HostSessionID,
		Model: "test-model", TemplateHashes: prepared.TemplateHashes,
		SystemInstructions: prepared.SystemPrompt,
	})
	require.NoError(t, err)

	crashingFS := &crashAfterFirstWorkspaceWriteFS{crashAfter: 1}
	initial.fs = crashingFS
	func() {
		defer func() {
			require.Equal(t, errSimulatedCommitProcessCrash, recover())
		}()
		_, commitErr := initial.CommitAttempt(ctx, prepared.AttemptID, []CommitFile{
			{Path: "src/first.go", Content: candidateExisting},
			{Path: "src/second.go", Content: candidateNew},
		}, "", CommitMetadata{})
		if commitErr != nil {
			panic(commitErr)
		}
	}()
	require.Equal(t, 1, crashingFS.writes)
	partiallyWritten, err := os.ReadFile(existingPath)
	require.NoError(t, err)
	require.Equal(t, candidateExisting, string(partiallyWritten),
		"fixture must die after the first candidate write reaches the workspace")
	_, err = os.Stat(newPath)
	require.ErrorIs(t, err, os.ErrNotExist,
		"fixture must die before the second candidate write")
	// A kill during the next atomic write may leave its deterministic staging
	// inode, but never partial bytes at the governed path. Recovery owns and
	// removes this journal-derived name.
	stagingPath := atomicWorkspaceStagingPath(newPath, hashWorkspaceContent([]byte(candidateNew)))
	require.NoError(t, os.WriteFile(stagingPath, []byte("partial staged bytes"), 0o600))
	require.FileExists(t, stagingPath)

	// A replacement process has only the repository and SQLite state. Next is
	// the production scheduling/recovery boundary and must repair both before it
	// offers another unit of work.
	require.NoError(t, initialStore.Close())
	restarted, restartedStore := open(OSFileSystem{})
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })
	next, err := restarted.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.False(t, next.Done)
	require.Len(t, next.Resources, 1)
	require.Equal(t, resourceID, next.Resources[0].ResourceID)
	require.Equal(t, StateErrored, next.Resources[0].State,
		"interrupted execution is terminal while the resource remains retryable")

	restored, err := os.ReadFile(existingPath)
	require.NoError(t, err)
	require.Equal(t, []byte(originalExisting), restored,
		"restart must restore exact pre-commit bytes")
	restoredInfo, err := os.Stat(existingPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o640), restoredInfo.Mode().Perm(),
		"restart must restore exact pre-commit permissions")
	_, err = os.Stat(newPath)
	require.ErrorIs(t, err, os.ErrNotExist,
		"restart must delete files that did not exist before the interrupted commit")

	manifest, err := restartedStore.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusFailed), manifest.Status)
	candidate, err := restartedStore.GetCandidateSetByAttempt(ctx, prepared.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "rejected", candidate.Status,
		"a partially installed candidate must never become accepted provenance")
	require.Contains(t, candidate.DispositionReason, "interrupted workspace commit")
	owned, err := restartedStore.GetGeneratedFiles(resourceID)
	require.NoError(t, err)
	require.Len(t, owned, 1,
		"recovery must preserve only the pre-crash accepted ownership")
	require.Equal(t, "src/first.go", owned[0].Path)
	require.Equal(t, hashWorkspaceContent([]byte(originalExisting)), owned[0].ContentHash,
		"accepted provenance must still match the restored workspace bytes")

	// Recovery is replay-safe. A second scheduling pass neither rewrites the
	// original nor changes the safe execution/candidate dispositions.
	next, err = restarted.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.False(t, next.Done)
	restoredAgain, err := os.ReadFile(existingPath)
	require.NoError(t, err)
	require.Equal(t, []byte(originalExisting), restoredAgain)
	_, err = os.Stat(newPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	manifestAgain, err := restartedStore.GetExecutionManifest(ctx, started.ID)
	require.NoError(t, err)
	require.Equal(t, string(execution.StatusFailed), manifestAgain.Status)
	candidateAgain, err := restartedStore.GetCandidateSetByAttempt(ctx, prepared.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "rejected", candidateAgain.Status)
	staging, err := filepath.Glob(filepath.Join(root, "src", ".crest-spec-stage-*.tmp"))
	require.NoError(t, err)
	require.Empty(t, staging, "restart recovery must remove deterministic atomic staging debris")
}
