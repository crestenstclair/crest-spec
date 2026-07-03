package spec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
)

// Adoption file discovery: when a resource has no tracked files (fresh adopt
// of an existing codebase), map it to on-disk files by unambiguous naming
// convention. Zero or multiple candidates are reported, never guessed.

func writeTree(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, p)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("// "+p+"\n"), 0o644))
	}
}

func TestDiscoverResourceFilesUnambiguousMatch(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "src/kernel/frequency.rs", "src/kernel/velocity.rs", "src/engine/voice.rs")

	r := cuepkg.Resource{ID: "valueObject.Kernel.Frequency", Kind: "valueObject"}
	files, err := DiscoverResourceFiles(OSFileSystem{}, filepath.Join(root, "src"), r, "rust")
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.True(t, filepath.IsAbs(files[0]) == filepath.IsAbs(filepath.Join(root, "src")),
		"paths keep the srcDir's absolute/relative form")
	assert.Equal(t, filepath.Join(root, "src", "kernel", "frequency.rs"), files[0])
}

func TestDiscoverResourceFilesAmbiguousReturnsNothing(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "src/kernel/voice.rs", "src/engine/voice.rs")

	r := cuepkg.Resource{ID: "aggregate.Engine.Voice", Kind: "aggregate"}
	files, err := DiscoverResourceFiles(OSFileSystem{}, filepath.Join(root, "src"), r, "rust")
	require.NoError(t, err)
	assert.Empty(t, files, "two candidates is ambiguity — report, never guess")
}

func TestDiscoverResourceFilesNoMatch(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, "src/kernel/velocity.rs")

	r := cuepkg.Resource{ID: "valueObject.Kernel.Frequency", Kind: "valueObject"}
	files, err := DiscoverResourceFiles(OSFileSystem{}, filepath.Join(root, "src"), r, "rust")
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestSnakeCaseResourceName(t *testing.T) {
	assert.Equal(t, "midi_event_kind", snakeCase("MidiEventKind"))
	assert.Equal(t, "frequency", snakeCase("Frequency"))
	assert.Equal(t, "eq_band", snakeCase("EqBand"))
}
