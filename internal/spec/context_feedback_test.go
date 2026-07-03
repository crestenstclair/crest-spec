package spec

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The retry loop's contract: after a rejected commit or a wave failure, the
// NEXT spec/context call must carry the failure and any triage guidance back
// to the generator. These tests pin that contract — they are the regression
// guard for the dead-path bug where WaveErrors/UserGuidance existed but were
// never assigned.

func TestContextInjectsLastErrorOnRetry(t *testing.T) {
	s, st := newTestSpecWithSession(t)

	require.NoError(t, s.store.UpdateSessionResourceState(
		st.sessionID, st.resourceID, "rejected",
		"cargo check failed: mismatched types in frequency.rs", "", 1, ""))

	res, err := s.Context(context.Background(), st.sessionID, st.resourceID)
	require.NoError(t, err)

	require.Contains(t, res.Prompt, "## Previous Errors",
		"prompt must carry the previous failure section on retry")
	require.Contains(t, res.Prompt, "cargo check failed: mismatched types in frequency.rs",
		"prompt must contain the actual failure text")
}

func TestContextInjectsOwnResolveGuidance(t *testing.T) {
	s, st := newTestSpecWithSession(t)

	// spec/resolve stores guidance as a note on the resource itself.
	require.NoError(t, s.Resolve(context.Background(), st.sessionID, st.resourceID,
		"use try_new, not new; the constructor must validate", ""))

	res, err := s.Context(context.Background(), st.sessionID, st.resourceID)
	require.NoError(t, err)

	require.Contains(t, res.Prompt, "## Guidance",
		"prompt must carry the guidance section when the resource has a note")
	require.Contains(t, res.Prompt, "use try_new, not new; the constructor must validate",
		"prompt must contain the resolve guidance verbatim")
}

func TestContextInjectsDesignContract(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	ctx := context.Background()

	contract := `{"behaviors":[{"description":"rejects out-of-range tones"}]}`
	require.NoError(t, s.DesignCommit(ctx, "Audio", contract))

	res, err := s.Context(ctx, st.sessionID, st.resourceID)
	require.NoError(t, err)

	require.Contains(t, res.Prompt, "## Bounded-Context Design Contract",
		"the committed design contract for the resource's context must reach the generator")
	require.Contains(t, res.Prompt, "rejects out-of-range tones")
}

// Iterate, don't regenerate: after a REJECTED commit, the next spec/context
// must serve the previous attempt's files in UPDATE mode so the generator
// fixes the minor bug in place instead of rewriting the resource from
// scratch. (UPDATE mode previously activated only for successfully-committed
// resources — a new resource's retry loop was blind to its own last attempt.)
func TestContextServesRejectedAttemptForIteration(t *testing.T) {
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
	ctx := context.Background()

	outDir := t.TempDir()
	attempt := "package out\n\nfunc Tone() float64 { return 44O.0 } // typo: letter O\n"
	res, err := s.Commit(ctx, st.sessionID, st.resourceID,
		[]CommitFile{{Path: outDir + "/tone.go", Content: attempt}}, "", "claude-sonnet-5")
	require.NoError(t, err)
	require.False(t, res.Committed, "the validation must reject this commit")

	ctxRes, err := s.Context(ctx, st.sessionID, st.resourceID)
	require.NoError(t, err)

	require.Contains(t, ctxRes.Prompt, "UPDATE MODE",
		"a retry after rejection must run in UPDATE mode — iterate on the attempt, not regenerate")
	require.Contains(t, ctxRes.Prompt, "typo: letter O",
		"the previous attempt's actual code must be in the retry prompt")
	require.Contains(t, ctxRes.Prompt, "## Previous Errors",
		"the failure must accompany the code so the fix is targeted")
}

// Adoption keeps file tracking: committing with NO files (crest-spec adopt,
// or re-settling after a strengthened validation) must preserve the
// resource's generated_files rows — they are what makes UPDATE-mode
// iteration possible.
func TestNoFileCommitPreservesFileTracking(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	ctx := context.Background()

	path := t.TempDir() + "/tone.go"
	_, err := s.Commit(ctx, st.sessionID, st.resourceID,
		[]CommitFile{{Path: path, Content: "package out\n"}}, "", "claude-sonnet-5")
	require.NoError(t, err)

	files, err := s.store.GetGeneratedFiles(st.resourceID)
	require.NoError(t, err)
	require.Len(t, files, 1)

	// Re-settle with no files (the adopt path).
	require.NoError(t, s.Resolve(ctx, st.sessionID, st.resourceID, "re-settle", ""))
	_, err = s.Commit(ctx, st.sessionID, st.resourceID, nil, "adopted", "claude-sonnet-5")
	require.NoError(t, err)

	files, err = s.store.GetGeneratedFiles(st.resourceID)
	require.NoError(t, err)
	require.Len(t, files, 1, "a no-file commit must not sever the resource from its files")
	require.Equal(t, path, files[0].Path)
}

func TestContextWithoutFailureHasNoErrorSection(t *testing.T) {
	s, st := newTestSpecWithSession(t)

	res, err := s.Context(context.Background(), st.sessionID, st.resourceID)
	require.NoError(t, err)

	require.False(t, strings.Contains(res.Prompt, "## Previous Errors"),
		"a clean first attempt must not carry an error section")
}
