package spec

import (
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
