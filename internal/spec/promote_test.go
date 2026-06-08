package spec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// promoteFakeStore satisfies specStore for promotion tests. It records every
// UpdateLearningStatus call so a preview run can assert nothing was mutated.
type promoteFakeStore struct {
	specStore
	active        []store.Learning
	statusUpdates []statusUpdate
}

type statusUpdate struct {
	id     string
	status string
}

func (f *promoteFakeStore) ListLearnings(status string) ([]store.Learning, error) {
	if status == "active" {
		return f.active, nil
	}
	return nil, nil
}

func (f *promoteFakeStore) UpdateLearningStatus(id, status string) error {
	f.statusUpdates = append(f.statusUpdates, statusUpdate{id: id, status: status})
	return nil
}

func promotableLearnings() []store.Learning {
	return []store.Learning{
		{ID: "a", Status: "active", Text: "use blocking send", Rationale: "avoids drops", Confidence: 0.9, TimesApplied: 5, ScopeLang: "rust"},
		{ID: "b", Status: "active", Text: "skip me", Confidence: 0.5, TimesApplied: 9, ScopeLang: "rust"}, // low confidence
		{ID: "c", Status: "active", Text: "cross-lang rule", Confidence: 0.95, TimesApplied: 4, ScopeLang: ""},
	}
}

func TestPromoteLearnings_PreviewWritesNothing(t *testing.T) {
	fake := &promoteFakeStore{active: promotableLearnings()}
	s := &Spec{store: fake, fs: OSFileSystem{}, cfg: &config.Config{}}

	target := filepath.Join(t.TempDir(), "learned", "rust.md")

	res, err := s.PromoteLearnings(context.Background(), "rust", 0, 0, false, target)
	require.NoError(t, err)

	assert.False(t, res.Applied)
	assert.Equal(t, target, res.TargetPath)
	require.Len(t, res.Promotable, 2) // "a" and cross-lang "c"; "b" filtered by confidence
	assert.Contains(t, res.MarkdownBlock, "- use blocking send")
	assert.Contains(t, res.MarkdownBlock, "  - avoids drops")
	assert.Contains(t, res.MarkdownBlock, "- cross-lang rule")

	// Nothing mutated.
	assert.Empty(t, fake.statusUpdates, "preview must not update learning status")
	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "preview must not create the template file")
}

func TestPromoteLearnings_ApplyAppendsAndMarksPromoted(t *testing.T) {
	fake := &promoteFakeStore{active: promotableLearnings()}
	s := &Spec{store: fake, fs: OSFileSystem{}, cfg: &config.Config{}}

	dir := t.TempDir()
	target := filepath.Join(dir, "learned", "rust.md")
	// Seed existing content with no trailing newline to exercise separation.
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("# existing"), 0o644))

	res, err := s.PromoteLearnings(context.Background(), "rust", 0, 0, true, target)
	require.NoError(t, err)
	assert.True(t, res.Applied)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "# existing")
	assert.Contains(t, content, "- use blocking send")
	assert.Contains(t, content, "- cross-lang rule")
	// Separation: existing content kept and a blank line precedes the block.
	assert.Contains(t, content, "# existing\n\n- ")

	// Both promotable learnings marked promoted.
	require.Len(t, fake.statusUpdates, 2)
	ids := map[string]string{}
	for _, u := range fake.statusUpdates {
		ids[u.id] = u.status
	}
	assert.Equal(t, "promoted", ids["a"])
	assert.Equal(t, "promoted", ids["c"])
}

func TestPromoteLearnings_DefaultPathAndLang(t *testing.T) {
	fake := &promoteFakeStore{active: nil}
	s := &Spec{store: fake, fs: OSFileSystem{}, cfg: &config.Config{}}

	res, err := s.PromoteLearnings(context.Background(), "", 0, 0, false, "")
	require.NoError(t, err)
	assert.Equal(t, "internal/prompt/templates/learned/rust.md", res.TargetPath)
	assert.Empty(t, res.MarkdownBlock)
}
