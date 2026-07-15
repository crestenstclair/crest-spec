package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProjectNormalizesContainedPaths(t *testing.T) {
	root := t.TempDir()
	project := FromRoot(root)

	resolved, err := project.Resolve("src/voice.go")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "src", "voice.go"), resolved)

	relative, err := project.Relative(filepath.Join(root, "src", "voice.go"))
	require.NoError(t, err)
	assert.Equal(t, "src/voice.go", relative)
}

func TestProjectRejectsLexicalEscapes(t *testing.T) {
	root := t.TempDir()
	project := FromRoot(root)

	for _, path := range []string{"../outside.go", root, filepath.Join(filepath.Dir(root), "outside.go")} {
		_, err := project.Resolve(path)
		require.ErrorContains(t, err, "outside project root", path)
	}
}

func TestProjectRejectsSymlinkEscapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows hosts")
	}
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "linked")))

	_, err := FromRoot(root).Resolve("linked/generated.go")
	require.ErrorContains(t, err, "resolves outside project root")
}

func TestFromSpecDirAnchorsRelativeConfiguration(t *testing.T) {
	workingDirectory, err := os.Getwd()
	require.NoError(t, err)
	project := FromSpecDir("spec")
	assert.Equal(t, workingDirectory, project.Root())
}
