package spec

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	cuepkg "github.com/crestenstclair/crest-spec/internal/cue"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// learningsFakeStore satisfies specStore for tests of buildRuntimeContext.
// Only the learnings methods are exercised; everything else delegates to the
// embedded nil interface and will panic only if unexpectedly called.
type learningsFakeStore struct {
	specStore
	learnings []store.Learning
	gotLang   string
	gotKind   string
}

func (f *learningsFakeStore) ListActiveLearnings(lang, kind string, limit int) ([]store.Learning, error) {
	f.gotLang, f.gotKind = lang, kind
	return f.learnings, nil
}
func (f *learningsFakeStore) IncrementLearningApplied(id string) error { return nil }
func (f *learningsFakeStore) GetGeneratedFiles(string) ([]store.GeneratedFile, error) {
	return nil, nil
}
func (f *learningsFakeStore) GetNote(string, string) (string, error) { return "", nil }

func TestBuildRuntimeContext_InjectsLearnings(t *testing.T) {
	fake := &learningsFakeStore{
		learnings: []store.Learning{
			{ID: "l1", Text: "prefer blocking send", ScopeLang: "rust", ScopeKind: "adapter"},
		},
	}
	cfg := &config.Config{SpecDir: t.TempDir() + "/spec"}
	s := &Spec{store: fake, cfg: cfg, fs: OSFileSystem{}}
	reg := &cuepkg.Registry{Project: &cuepkg.Project{Meta: cuepkg.Meta{Language: "rust"}}}
	res := cuepkg.Resource{ID: "adapter.Foo", Kind: "adapter"}

	ctx, err := s.buildRuntimeContext(res, reg, "apply1")
	require.NoError(t, err)
	require.Contains(t, ctx.Learnings, "prefer blocking send")
	assert.Equal(t, "rust", fake.gotLang)
	assert.Equal(t, "adapter", fake.gotKind)
}

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
