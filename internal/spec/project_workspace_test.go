package spec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/contextmanifest"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failAfterFirstDestroyUnlinkFS struct {
	OSFileSystem
	failed bool
}

func (f *failAfterFirstDestroyUnlinkFS) Remove(path string) error {
	if err := f.OSFileSystem.Remove(path); err != nil {
		return err
	}
	if !f.failed {
		f.failed = true
		return errors.New("simulated process failure after first destroy unlink")
	}
	return nil
}

func TestCandidateSnapshotUsesProjectWorkspaceForCreateDetection(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "src", "voice.go"), []byte("package voice\n"), 0o644))
	s := &Spec{fs: OSFileSystem{}, cfg: &config.Config{SpecDir: filepath.Join(root, "spec")}}

	normalized, err := s.normalizeCommitFiles([]CommitFile{{Path: "src/voice.go", Content: "package voice\n// changed\n"}})
	require.NoError(t, err)
	snapshot, err := s.snapshotCommitFiles(normalized)
	require.NoError(t, err)
	require.Len(t, snapshot, 1)
	assert.Equal(t, "src/voice.go", snapshot[0].Path)
	assert.Equal(t, "modify", snapshot[0].WriteIntent)
}

func TestCandidateNormalizationRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows hosts")
	}
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linked")))
	s := &Spec{fs: OSFileSystem{}, cfg: &config.Config{SpecDir: filepath.Join(root, "spec")}}

	_, err := s.normalizeCommitFiles([]CommitFile{{Path: "linked/generated.go", Content: "package generated\n"}})
	require.ErrorContains(t, err, "resolves outside project root")
}

func TestDestroyRejectsPersistedPathOutsideProject(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	require.NoError(t, os.MkdirAll(root, 0o755))
	outside := filepath.Join(parent, "outside.go")
	require.NoError(t, os.WriteFile(outside, []byte("package outside\n"), 0o644))

	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	require.NoError(t, st.CreateApply("apply-workspace", "spec-hash"))
	require.NoError(t, st.SetResource(store.Resource{ID: "resource.outside", Kind: "asset"}))
	require.NoError(t, st.SetGeneratedFile(store.GeneratedFile{Path: "../outside.go", ResourceID: "resource.outside"}))
	s := New(st, OSFileSystem{}, &config.Config{SpecDir: filepath.Join(root, "spec")})

	destroyed, err := s.executeDestroys(context.Background(), []planpkg.PlannedAction{{
		ResourceID: "resource.outside", Kind: planpkg.ActionDestroy,
	}}, "apply-workspace")
	require.ErrorContains(t, err, "outside project root")
	assert.Empty(t, destroyed)
	require.FileExists(t, outside)
	_, err = st.GetResource("resource.outside")
	require.NoError(t, err)
}

func TestDestroyWaitsForProjectCommitLockBeforeMutatingGovernedFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "spec"), 0o755))
	target := filepath.Join(root, "owned.txt")
	require.NoError(t, os.WriteFile(target, []byte("governed\n"), 0o644))

	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, st.Close()) })
	require.NoError(t, st.CreateApply("apply-destroy-lock", "spec-hash"))
	require.NoError(t, st.SetResource(store.Resource{ID: "resource.destroy", Kind: "asset"}))
	require.NoError(t, st.SetGeneratedFile(store.GeneratedFile{
		Path: "owned.txt", ResourceID: "resource.destroy", ContentHash: contextmanifest.Hash("governed\n"),
	}))
	s := New(st, OSFileSystem{}, &config.Config{SpecDir: filepath.Join(root, "spec")})

	releaseCommit, err := s.projectWorkspace().AcquireCommit(ctx)
	require.NoError(t, err)
	defer releaseCommit()
	type destroyResult struct {
		destroyed []DestroyedResource
		err       error
	}
	done := make(chan destroyResult, 1)
	go func() {
		destroyed, destroyErr := s.executeDestroys(ctx, []planpkg.PlannedAction{{
			ResourceID: "resource.destroy", Kind: planpkg.ActionDestroy,
		}}, "apply-destroy-lock")
		done <- destroyResult{destroyed: destroyed, err: destroyErr}
	}()

	select {
	case premature := <-done:
		t.Fatalf("destroy mutated governance while another commit owned the project lock: %+v", premature)
	case <-time.After(100 * time.Millisecond):
	}
	require.FileExists(t, target)
	_, err = st.GetResource("resource.destroy")
	require.NoError(t, err)

	releaseCommit()
	result := <-done
	require.NoError(t, result.err)
	require.Len(t, result.destroyed, 1)
	require.NoFileExists(t, target)
	_, err = st.GetResource("resource.destroy")
	require.Error(t, err)
}

func TestDestroyRefusesDivergedAcceptedFileBeforeDeletingAnything(t *testing.T) {
	ctx := context.Background()
	s, state := newTestSpecWithSession(t)
	projectRoot := filepath.Dir(s.cfg.SpecDir)
	cleanPath := filepath.Join(projectRoot, "generated", "a-clean.go")
	editedPath := filepath.Join(projectRoot, "generated", "z-edited.go")
	const cleanContent = "package generated\n\nconst Clean = true\n"
	const originalEditedContent = "package generated\n\nconst Edited = false\n"
	const userEditedContent = "package generated\n\nconst Edited = \"user change\"\n"
	_, err := commitTestAttempt(
		t, s, ctx, state.sessionID, state.resourceID,
		[]CommitFile{
			{Path: "generated/a-clean.go", Content: cleanContent},
			{Path: "generated/z-edited.go", Content: originalEditedContent},
		},
		"", "test-model",
	)
	require.NoError(t, err)
	beforeOwnership, err := s.store.GetGeneratedFiles(state.resourceID)
	require.NoError(t, err)
	require.Len(t, beforeOwnership, 2)
	require.NoError(t, os.WriteFile(editedPath, []byte(userEditedContent), 0o644))

	session, err := s.store.GetSession(state.sessionID)
	require.NoError(t, err)
	destroyed, err := s.executeDestroys(ctx, []planpkg.PlannedAction{{
		ResourceID: state.resourceID, Kind: planpkg.ActionDestroy,
	}}, session.ApplyID)
	require.Empty(t, destroyed)
	require.ErrorContains(t, err, "generated/z-edited.go")
	require.ErrorContains(t, err, "changed since CrestSpec accepted it")

	cleanBytes, err := os.ReadFile(cleanPath)
	require.NoError(t, err, "destroy must preflight every path before deleting the first one")
	require.Equal(t, cleanContent, string(cleanBytes))
	editedBytes, err := os.ReadFile(editedPath)
	require.NoError(t, err)
	require.Equal(t, userEditedContent, string(editedBytes), "user edits must remain untouched")
	_, err = s.store.GetResource(state.resourceID)
	require.NoError(t, err, "resource row must remain when destroy fails closed")
	afterOwnership, err := s.store.GetGeneratedFiles(state.resourceID)
	require.NoError(t, err)
	require.Equal(t, beforeOwnership, afterOwnership, "all generated-file ownership rows must remain unchanged")
}

func TestDestroyRetryConvergesAfterFailureFollowingFirstUnlink(t *testing.T) {
	ctx := context.Background()
	s, state := newTestSpecWithSession(t)
	projectRoot := filepath.Dir(s.cfg.SpecDir)
	firstPath := filepath.Join(projectRoot, "generated", "first.go")
	secondPath := filepath.Join(projectRoot, "generated", "second.go")
	_, err := commitTestAttempt(
		t, s, ctx, state.sessionID, state.resourceID,
		[]CommitFile{
			{Path: "generated/first.go", Content: "package generated\n\nconst First = true\n"},
			{Path: "generated/second.go", Content: "package generated\n\nconst Second = true\n"},
		},
		"", "test-model",
	)
	require.NoError(t, err)
	session, err := s.store.GetSession(state.sessionID)
	require.NoError(t, err)
	s.fs = &failAfterFirstDestroyUnlinkFS{}
	action := []planpkg.PlannedAction{{ResourceID: state.resourceID, Kind: planpkg.ActionDestroy}}

	destroyed, err := s.executeDestroys(ctx, action, session.ApplyID)
	require.Empty(t, destroyed)
	require.ErrorContains(t, err, "simulated process failure after first destroy unlink")
	remaining := 0
	for _, path := range []string{firstPath, secondPath} {
		if _, statErr := os.Stat(path); statErr == nil {
			remaining++
		} else {
			require.ErrorIs(t, statErr, os.ErrNotExist)
		}
	}
	require.Equal(t, 1, remaining, "fault must occur after exactly one accepted file was unlinked")
	_, err = s.store.GetResource(state.resourceID)
	require.NoError(t, err)
	ownership, err := s.store.GetGeneratedFiles(state.resourceID)
	require.NoError(t, err)
	require.Len(t, ownership, 2, "failed destroy must retain durable ownership for retry")

	destroyed, err = s.executeDestroys(ctx, action, session.ApplyID)
	require.NoError(t, err)
	require.Len(t, destroyed, 1)
	require.NoFileExists(t, firstPath)
	require.NoFileExists(t, secondPath)
	_, err = s.store.GetResource(state.resourceID)
	require.Error(t, err)
	ownership, err = s.store.GetGeneratedFiles(state.resourceID)
	require.NoError(t, err)
	require.Empty(t, ownership)
	actions, err := s.store.ListApplyActions(session.ApplyID)
	require.NoError(t, err)
	var destroyOutcomes []string
	for _, recorded := range actions {
		if recorded.ResourceID == state.resourceID && recorded.Action == "destroy" {
			destroyOutcomes = append(destroyOutcomes, recorded.Outcome)
		}
	}
	require.Equal(t, []string{"failed", "destroyed"}, destroyOutcomes)
}
