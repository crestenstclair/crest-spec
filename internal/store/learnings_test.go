package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(t.TempDir() + "/test.db")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLearnings_CreateAndListByScope(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	require.NoError(t, s.CreateLearning(Learning{
		ID: "l1", ScopeLang: "rust", ScopeKind: "adapter",
		Text: "prefer blocking send", Confidence: 0.9, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, s.CreateLearning(Learning{
		ID: "l2", ScopeLang: "rust", ScopeKind: "", // global rust
		Text: "use crate:: paths", Confidence: 0.5, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, s.CreateLearning(Learning{
		ID: "l3", ScopeLang: "go", ScopeKind: "adapter",
		Text: "irrelevant", Confidence: 0.9, Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}))

	got, err := s.ListActiveLearnings("rust", "adapter", 10)
	require.NoError(t, err)
	// l1 (rust/adapter) and l2 (rust/global) match; l3 (go) does not.
	require.Len(t, got, 2)
	assert.Equal(t, "l1", got[0].ID) // higher confidence first
	assert.Equal(t, "l2", got[1].ID)
}

func TestLearnings_RetireExcludesFromActive(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	require.NoError(t, s.CreateLearning(Learning{ID: "l1", ScopeLang: "rust", Text: "x", Status: "active", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, s.UpdateLearningStatus("l1", "retired"))
	got, err := s.ListActiveLearnings("rust", "adapter", 10)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestLearnings_IncrementApplied(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	require.NoError(t, s.CreateLearning(Learning{ID: "l1", ScopeLang: "rust", Text: "x", Status: "active", CreatedAt: now, UpdatedAt: now}))
	require.NoError(t, s.IncrementLearningApplied("l1"))
	rows, err := s.ListLearnings("active")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 1, rows[0].TimesApplied)
}
