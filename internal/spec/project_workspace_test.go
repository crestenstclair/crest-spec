package spec

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/config"
	planpkg "github.com/crestenstclair/crest-spec/internal/plan"
	"github.com/crestenstclair/crest-spec/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

	destroyed, err := s.executeDestroys([]planpkg.PlannedAction{{
		ResourceID: "resource.outside", Kind: planpkg.ActionDestroy,
	}}, "apply-workspace")
	require.ErrorContains(t, err, "outside project root")
	assert.Empty(t, destroyed)
	require.FileExists(t, outside)
	_, err = st.GetResource("resource.outside")
	require.NoError(t, err)
}
