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

// commitTestSpecCUE declares one value object with no resource-level
// validation, so a commit against it exercises the no-gate path.
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
	return newTestSpecWithSessionCUE(t, commitTestSpecCUE)
}

func newTestSpecWithSessionCUE(t *testing.T, specCUE string) (*Spec, commitTestSession) {
	t.Helper()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spec.cue"), []byte(specCUE), 0o644))

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

// A validation that cannot LAUNCH (missing binary, bad path) must reject the
// commit, not skip the gate. This pins the fail-closed contract: an unrunnable
// gate is a failed gate.
func TestCommitRejectsWhenValidationCannotRun(t *testing.T) {
	const cueWithBrokenValidation = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	validations: [{kind: "custom", command: ["/nonexistent-crest-validator-xyz"]}]
}
`
	s, st := newTestSpecWithSessionCUE(t, cueWithBrokenValidation)
	res, err := s.Commit(context.Background(), st.sessionID, st.resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}, "",
		"claude-sonnet-4-6")
	require.NoError(t, err)
	require.False(t, res.Committed,
		"a validation that cannot run must reject the commit (fail closed), not skip the gate")
	require.NotEmpty(t, res.Validations, "the rejection must carry a validation result explaining the launch failure")
	require.False(t, res.Validations[0].Passed)
	require.Contains(t, res.Validations[0].Message, "could not run")
}

func TestCommitRecordsGenerationOutcome(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	_, err := s.Commit(context.Background(), st.sessionID, st.resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}, "", "claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	gens, _ := s.store.ListGenerations(st.resourceID, 10)
	if len(gens) == 0 {
		t.Fatal("expected a generation record from commit")
	}
	// Outcome must round-trip through the DB CHECK constraint
	// (outcome IN ('accepted','rejected')) — store errors are swallowed by
	// convention, so a bad value silently leaves outcome NULL.
	require.Equal(t, "accepted", gens[0].Outcome, "committed generation must record outcome 'accepted'")
}

func TestCommitRejectedGenerationOutcomeRoundTrips(t *testing.T) {
	const cueWithFailingValidation = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	validations: [{kind: "custom", command: ["false"]}]
}
`
	s, st := newTestSpecWithSessionCUE(t, cueWithFailingValidation)
	_, err := s.Commit(context.Background(), st.sessionID, st.resourceID,
		[]CommitFile{{Path: filepath.Join(t.TempDir(), "a.go"), Content: "package out\n"}}, "",
		"claude-sonnet-4-6")
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	gens, _ := s.store.ListGenerations(st.resourceID, 10)
	if len(gens) == 0 {
		t.Fatal("expected a generation record from rejected commit")
	}
	require.Equal(t, "rejected", gens[0].Outcome, "rejected generation must record outcome 'rejected'")
}
