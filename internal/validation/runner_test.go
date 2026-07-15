package validation

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteCapturesBoundedProvenanceAndArtifacts(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CREST_TEST_VALUE", "sensitive-value")
	require.NoError(t, os.WriteFile(filepath.Join(root, "source.txt"), []byte("source"), 0o644))
	result := Execute(t.Context(), Options{
		ProjectRoot:  root,
		Command:      []string{"sh", "-c", "printf 123456789; printf error >&2; printf artifact > result.txt"},
		Artifacts:    []string{"result.txt", "missing.txt"},
		Environment:  []string{"CREST_TEST_VALUE"},
		CaptureBytes: 5,
	})

	assert.Empty(t, result.LaunchError)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "12345", result.Stdout)
	assert.Equal(t, int64(9), result.StdoutBytes)
	assert.True(t, result.StdoutTruncated)
	assert.Equal(t, "error", result.Stderr)
	assert.NotEmpty(t, result.CommandHash)
	assert.NotEmpty(t, result.EnvironmentHash)
	assert.NotContains(t, result.EnvironmentJSON, os.Getenv("CREST_TEST_VALUE"))
	require.Len(t, result.Artifacts, 2)
	assert.Equal(t, "artif", result.Artifacts[0].Content)
	assert.True(t, result.Artifacts[0].Truncated)
	assert.True(t, result.Artifacts[1].Missing)
}

func TestExecuteTimesOutAndRejectsEscapingWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	timedOut := Execute(t.Context(), Options{
		ProjectRoot: root, Command: []string{"sh", "-c", "sleep 1"}, Timeout: 10 * time.Millisecond,
	})
	assert.True(t, timedOut.TimedOut)
	assert.NotEqual(t, 0, timedOut.ExitCode)

	escape := Execute(t.Context(), Options{
		ProjectRoot: root, WorkingDirectory: "../outside", Command: []string{"true"},
	})
	assert.Contains(t, escape.LaunchError, "escapes")
}

func TestHashSourceTreeIgnoresOperationalAndBuildOutputs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main"), 0o644))
	first, err := HashSourceTree(root)
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(filepath.Join(root, "target"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "target", "output"), []byte("ignored"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "crest.db"), []byte("ignored"), 0o644))
	second, err := HashSourceTree(root)
	require.NoError(t, err)
	assert.Equal(t, first, second)
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package changed"), 0o644))
	third, err := HashSourceTree(root)
	require.NoError(t, err)
	assert.NotEqual(t, first, third)
}
