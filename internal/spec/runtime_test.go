package spec

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildModuleTree(t *testing.T) {
	dir := t.TempDir()
	fs := OSFileSystem{}

	require.NoError(t, fs.MkdirAll(dir+"/src/Synth/Voice", 0o755))
	require.NoError(t, fs.WriteFile(dir+"/src/Synth/Voice/voice.rs", []byte("code"), 0o644))
	require.NoError(t, fs.WriteFile(dir+"/src/Synth/Voice/voice_test.rs", []byte("test"), 0o644))

	tree, err := buildModuleTree(fs, dir+"/src")
	require.NoError(t, err)
	assert.Contains(t, tree, "Synth")
	assert.Contains(t, tree, "Voice")
	assert.Contains(t, tree, "voice.rs")
}

func TestBuildModuleTree_EmptyDir(t *testing.T) {
	tree, err := buildModuleTree(OSFileSystem{}, "/nonexistent/path")
	require.NoError(t, err)
	assert.Equal(t, "", tree)
}

// collectModuleFiles must surface both the src/ module declarations and the
// project-root Cargo.toml so the model can extend the manifest with new crate
// dependencies (e.g. when generating adapters in later waves) instead of
// regenerating it from scratch and dropping existing deps.
func TestCollectModuleFiles_RustModulesAndManifest(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	fs := OSFileSystem{}

	require.NoError(t, fs.MkdirAll(filepath.Join(src, "kernel"), 0o755))
	require.NoError(t, fs.WriteFile(filepath.Join(root, "Cargo.toml"), []byte("[package]\nname = \"x\"\n"), 0o644))
	require.NoError(t, fs.WriteFile(filepath.Join(src, "lib.rs"), []byte("pub mod kernel;\n"), 0o644))
	require.NoError(t, fs.WriteFile(filepath.Join(src, "kernel", "mod.rs"), []byte("pub mod note_id;\n"), 0o644))

	got := collectModuleFiles(fs, src, "rust")

	require.Contains(t, got, "Cargo.toml")
	assert.Contains(t, got["Cargo.toml"], "[package]")
	assert.Contains(t, got, filepath.Join("src", "lib.rs"))
	assert.Contains(t, got, filepath.Join("src", "kernel", "mod.rs"))
}

func TestCollectModuleFiles_UnsupportedLanguage(t *testing.T) {
	assert.Nil(t, collectModuleFiles(OSFileSystem{}, t.TempDir(), "cobol"))
}
