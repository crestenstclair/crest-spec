package spec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// commitTestSpecCUE declares one value object with no resource-level validation,
// so the only thing that can gate a commit is a supplied invariant verdict.
const commitTestSpecCUE = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {from: "f64"}
project: invariants: [{text: "no global state"}]
`

// commitTestSession bundles the IDs the commit-contract tests assert against.
type commitTestSession struct {
	sessionID  string
	resourceID string
}

// newTestSpecWithSession builds a Spec over a real store + minimal spec and
// starts a session, returning the session and a generatable resource ID.
func newTestSpecWithSession(t *testing.T) (*Spec, commitTestSession) {
	t.Helper()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spec.cue"), []byte(commitTestSpecCUE), 0o644))

	cfg := &config.Config{SpecDir: dir, GenerateModel: "claude-sonnet-4-6", MaxRetries: 3}
	s := New(st, OSFileSystem{}, cfg)

	ctx := context.Background()
	begin, err := s.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.NotEmpty(t, begin.SessionID, "expected a session to start")

	next, err := s.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.False(t, next.Done, "expected at least one wave of work")
	require.NotEmpty(t, next.Resources, "expected a generatable resource")

	return s, commitTestSession{sessionID: begin.SessionID, resourceID: next.Resources[0].ResourceID}
}

func TestCommitRejectsOnFailedInvariantVerdict(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	res, err := s.Commit(context.Background(), st.sessionID, st.resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}, "",
		[]InvariantCheckInput{{Invariant: "no global state", Passed: false, Summary: "uses a package-level var"}},
		"claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if res.Committed {
		t.Fatal("commit should be rejected when a supplied invariant verdict failed")
	}
	found := false
	for _, v := range res.Validations {
		if v.Kind == "invariant" && !v.Passed {
			found = true
		}
	}
	if !found {
		t.Fatal("expected a failed invariant ValidationResult")
	}
}

func TestCommitRecordsInvariantVerdict(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	_, err := s.Commit(context.Background(), st.sessionID, st.resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}, "",
		[]InvariantCheckInput{{Invariant: "no global state", Passed: true, Summary: "clean"}},
		"claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	sess, err := s.store.GetSession(st.sessionID)
	require.NoError(t, err)
	checks, err := s.store.ListInvariantChecks(sess.ApplyID)
	require.NoError(t, err)
	if len(checks) == 0 {
		t.Fatal("expected the supplied invariant verdict to be recorded")
	}
}

func TestCommitRecordsGenerationOutcome(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	_, err := s.Commit(context.Background(), st.sessionID, st.resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}, "", nil, "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	gens, _ := s.store.ListGenerations(st.resourceID, 10)
	if len(gens) == 0 {
		t.Fatal("expected a generation record from commit")
	}
}
