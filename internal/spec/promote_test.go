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
	proposal      *store.EvaluationPromotionProposal
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

func (f *promoteFakeStore) CreateEvaluationPromotionProposal(_ context.Context, runID, variantName, changeKind, targetIdentity string, change map[string]any, rollbackIdentity string) (*store.EvaluationPromotionProposal, error) {
	f.proposal = &store.EvaluationPromotionProposal{
		ID: "proposal-1", RunID: runID, VariantName: variantName, ChangeKind: changeKind,
		TargetIdentity: targetIdentity, Change: change, RollbackIdentity: rollbackIdentity,
		Status: "eligible", EligibilityReason: "qualifying fixture evaluation",
	}
	return f.proposal, nil
}

func (f *promoteFakeStore) GetEvaluationPromotionProposal(_ context.Context, _ string) (*store.EvaluationPromotionProposal, error) {
	return f.proposal, nil
}

func (f *promoteFakeStore) RecordEvaluationPromotionDecision(_ context.Context, _ string, decision, _, _ string) (*store.EvaluationPromotionProposal, error) {
	f.proposal.Status = decision
	return f.proposal, nil
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

func TestPromoteLearnings_ThresholdOnlyApplyIsBlocked(t *testing.T) {
	fake := &promoteFakeStore{active: promotableLearnings()}
	s := &Spec{store: fake, fs: OSFileSystem{}, cfg: &config.Config{}}

	dir := t.TempDir()
	target := filepath.Join(dir, "learned", "rust.md")
	// Seed existing content with no trailing newline to exercise separation.
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("# existing"), 0o644))

	res, err := s.PromoteLearnings(context.Background(), "rust", 0, 0, true, target)
	require.Error(t, err)
	assert.False(t, res.Applied)
	assert.Contains(t, err.Error(), "evaluation-backed proposal")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "# existing", string(data))
	assert.Empty(t, fake.statusUpdates)
}

func TestLearningPromotion_ProposeApproveAndApplyExactChange(t *testing.T) {
	fake := &promoteFakeStore{active: promotableLearnings()}
	s := &Spec{store: fake, fs: OSFileSystem{}, cfg: &config.Config{}}
	target := filepath.Join(t.TempDir(), "learned", "rust.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("# existing"), 0o644))

	proposed, err := s.ProposeLearningPromotion(context.Background(), "rust", 0, 0, target, "run-1", "candidate")
	require.NoError(t, err)
	require.NotNil(t, proposed.Proposal)
	assert.Equal(t, "eligible", proposed.Proposal.Status)
	assert.Equal(t, target, proposed.Proposal.TargetIdentity)

	approved, err := s.DecideLearningPromotion(context.Background(), proposed.Proposal.ID, "approved", "reviewer", "held-out evidence passed")
	require.NoError(t, err)
	assert.Equal(t, "approved", approved.Status)
	applied, err := s.ApplyLearningPromotion(context.Background(), proposed.Proposal.ID, "crest-spec", "applied reviewed proposal")
	require.NoError(t, err)
	assert.Equal(t, "applied", applied.Status)

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Contains(t, string(data), "# existing\n\n- use blocking send")
	require.Len(t, fake.statusUpdates, 2)
}

func TestPromoteLearnings_DefaultPathAndLang(t *testing.T) {
	fake := &promoteFakeStore{active: nil}
	s := &Spec{store: fake, fs: OSFileSystem{}, cfg: &config.Config{}}

	res, err := s.PromoteLearnings(context.Background(), "", 0, 0, false, "")
	require.NoError(t, err)
	assert.Equal(t, "internal/prompt/templates/learned/rust.md", res.TargetPath)
	assert.Empty(t, res.MarkdownBlock)
}
