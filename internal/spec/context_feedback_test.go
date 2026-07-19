package spec

import (
	"context"
	"os"
	"path/filepath"
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
	res, err := commitTestAttempt(t, s, ctx, st.sessionID, st.resourceID,
		[]CommitFile{{Path: outDir + "/tone.go", Content: attempt}}, "", "claude-sonnet-5")
	require.NoError(t, err)
	require.True(t, res.Committed)
	require.True(t, res.ValidationDeferred)
	sr, err := s.store.GetSessionResource(st.sessionID, st.resourceID)
	require.NoError(t, err)
	verified := s.VerifyWave(ctx, st.sessionID, sr.WaveIndex)
	require.False(t, verified.Passed, "the final validation batch must reopen the staged candidate")

	ctxRes, err := s.Context(ctx, st.sessionID, st.resourceID)
	require.NoError(t, err)
	require.Equal(t, "integration_implementer", ctxRes.Role)
	require.NotEmpty(t, ctxRes.HandoffID)

	require.Contains(t, ctxRes.Prompt, "UPDATE MODE",
		"a retry after rejection must run in UPDATE mode — iterate on the attempt, not regenerate")
	require.Contains(t, ctxRes.Prompt, "typo: letter O",
		"the previous attempt's actual code must be in the retry prompt")
	require.Contains(t, ctxRes.Prompt, "## Previous Errors",
		"the failure must accompany the code so the fix is targeted")
}

func TestResolvedMisattributedValidationFailureReinstatesAcceptedCandidateWithoutRetry(t *testing.T) {
	const cueWithFailingValidation = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	validations: [{kind: "custom", command: ["false"]}]
}
`
	s, state := newTestSpecWithSessionCUE(t, cueWithFailingValidation)
	ctx := context.Background()
	path := t.TempDir() + "/tone.go"
	commit, err := commitTestAttempt(t, s, ctx, state.sessionID, state.resourceID,
		[]CommitFile{{Path: path, Content: "package out\n"}}, "", "test-model")
	require.NoError(t, err)
	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	verified := s.VerifyWave(ctx, state.sessionID, resource.WaveIndex)
	require.False(t, verified.Passed)
	require.Equal(t, []string{state.resourceID}, verified.ReopenedResources)

	failures, err := s.store.ListFailureClassificationsByAttempt(ctx, commit.AttemptID)
	require.NoError(t, err)
	require.Len(t, failures, 1)
	handoffs, err := s.store.ListAttemptHandoffsBySource(ctx, commit.AttemptID)
	require.NoError(t, err)
	require.Len(t, handoffs, 1)
	require.Equal(t, "pending", handoffs[0].Status)
	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.NoError(t, s.store.UpdateSessionResourceState(
		state.sessionID, state.resourceID, string(StatePending), resource.LastError, "", resource.MaxRetries, "",
	))
	require.NoError(t, s.ResolveFailure(ctx, failures[0].ID, "triage proved the diagnostic belongs to another owner", ""))
	require.NoError(t, s.Resolve(ctx, state.sessionID, state.resourceID, "preserve the exact accepted candidate", ""))
	attemptsBefore, err := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, err)
	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Empty(t, resource.LastError)
	failure, err := s.store.GetFailureClassification(ctx, failures[0].ID)
	require.NoError(t, err)
	require.NotNil(t, failure.ResolvedAt)
	require.Empty(t, failure.ResolvedByAttempt)
	candidate, err := s.store.GetCandidateSetByAttempt(ctx, commit.AttemptID)
	require.NoError(t, err)
	plan, err := s.Plan(ctx)
	require.NoError(t, err)
	require.Equal(t, plan.GenerationInputHashes[state.resourceID], candidate.GenerationInputHash)
	require.Equal(t, plan.DependencyContractHashes[state.resourceID], candidate.DependencyContractHash)
	matches, err := s.candidateMatchesOwnedWorkspace(state.resourceID, candidate)
	require.NoError(t, err)
	require.True(t, matches)

	result, err := s.Context(ctx, state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.True(t, result.NoDispatch)
	require.Equal(t, commit.CandidateID, result.ReinstatedCandidateID)
	require.Empty(t, result.AttemptID, "reinstatement must not create a generation attempt")
	require.Contains(t, result.Instructions, "Call spec/next")
	attemptsAfter, err := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfter, len(attemptsBefore), "reinstatement must not consume a retry")

	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), resource.State)
	require.Equal(t, "integration_pending", resource.Phase)
	require.Equal(t, resource.MaxRetries, resource.Attempts, "the historical retry count is preserved")
	handoffs, err = s.store.ListAttemptHandoffsBySource(ctx, commit.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", handoffs[0].Status)
	next, err := s.Next(ctx, state.sessionID)
	require.NoError(t, err)
	require.True(t, next.ValidationReady)

	session, err := s.store.GetSession(state.sessionID)
	require.NoError(t, err)
	actions, err := s.store.ListApplyActions(session.ApplyID)
	require.NoError(t, err)
	require.Equal(t, "reinstate_candidate", actions[len(actions)-1].Action)
	require.Equal(t, "reinstated", actions[len(actions)-1].Outcome)
}

func TestNextReinstatesResolvedAcceptedCandidateBeforeWaveSelection(t *testing.T) {
	const cueWithFailingValidation = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	validations: [{kind: "custom", command: ["false"]}]
}
`
	s, state := newTestSpecWithSessionCUE(t, cueWithFailingValidation)
	ctx := context.Background()
	commit, err := commitTestAttempt(t, s, ctx, state.sessionID, state.resourceID,
		[]CommitFile{{Path: t.TempDir() + "/tone.go", Content: "package out\n"}}, "", "test-model")
	require.NoError(t, err)
	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	verified := s.VerifyWave(ctx, state.sessionID, resource.WaveIndex)
	require.False(t, verified.Passed)
	failures, err := s.store.ListFailureClassificationsByAttempt(ctx, commit.AttemptID)
	require.NoError(t, err)
	require.Len(t, failures, 1)
	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.NoError(t, s.store.UpdateSessionResourceState(
		state.sessionID, state.resourceID, string(StatePending), resource.LastError, "", resource.MaxRetries, "",
	))
	require.NoError(t, s.ResolveFailure(ctx, failures[0].ID, "diagnostic was misattributed", ""))
	require.NoError(t, s.Resolve(ctx, state.sessionID, state.resourceID, "preserve accepted candidate", ""))
	attemptsBefore, err := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, err)

	next, err := s.Next(ctx, state.sessionID)
	require.NoError(t, err)
	require.True(t, next.ValidationReady)
	require.Empty(t, next.Resources)
	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), resource.State)
	require.Equal(t, resource.MaxRetries, resource.Attempts)
	attemptsAfter, err := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfter, len(attemptsBefore))
}

func TestResolvedFailureDoesNotReinstateCandidateWhenWorkspaceDiverged(t *testing.T) {
	const cueWithFailingValidation = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: Tone: {
	from: "f64"
	validations: [{kind: "custom", command: ["false"]}]
}
`
	s, state := newTestSpecWithSessionCUE(t, cueWithFailingValidation)
	ctx := context.Background()
	path := t.TempDir() + "/tone.go"
	commit, err := commitTestAttempt(t, s, ctx, state.sessionID, state.resourceID,
		[]CommitFile{{Path: path, Content: "package out\n"}}, "", "test-model")
	require.NoError(t, err)
	resource, err := s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	verified := s.VerifyWave(ctx, state.sessionID, resource.WaveIndex)
	require.False(t, verified.Passed)
	failures, err := s.store.ListFailureClassificationsByAttempt(ctx, commit.AttemptID)
	require.NoError(t, err)
	require.Len(t, failures, 1)
	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.NoError(t, s.store.UpdateSessionResourceState(
		state.sessionID, state.resourceID, string(StatePending), resource.LastError, "", resource.MaxRetries, "",
	))
	require.NoError(t, s.ResolveFailure(ctx, failures[0].ID, "diagnostic was misattributed", ""))
	require.NoError(t, s.Resolve(ctx, state.sessionID, state.resourceID, "preserve only if bytes still match", ""))
	require.NoError(t, os.WriteFile(path, []byte("package out\n// changed outside the candidate\n"), 0o644))
	attemptsBefore, err := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, err)

	result, err := s.Context(ctx, state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.NoDispatch,
		"workspace divergence must require a fresh governed repair instead of reinstating the old candidate")
	require.NotEqual(t, commit.AttemptID, result.AttemptID)
	require.Contains(t, result.Prompt, "changed outside the candidate")
	attemptsAfter, err := s.store.ListGenerationAttemptsBySession(ctx, state.sessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfter, len(attemptsBefore)+1)
	resource, err = s.store.GetSessionResource(state.sessionID, state.resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StatePending), resource.State)
	require.Equal(t, "integration_failed", resource.Phase)
	handoffs, err := s.store.ListAttemptHandoffsBySource(ctx, commit.AttemptID)
	require.NoError(t, err)
	require.Len(t, handoffs, 1)
	require.Equal(t, "accepted", handoffs[0].Status)
	require.Equal(t, result.AttemptID, handoffs[0].TargetAttemptID)
}

// Adoption keeps file tracking: committing with NO files (crest-spec adopt,
// or re-settling after a strengthened validation) must preserve the
// resource's generated_files rows — they are what makes UPDATE-mode
// iteration possible.
func TestNoFileCommitPreservesFileTracking(t *testing.T) {
	s, st := newTestSpecWithSession(t)
	ctx := context.Background()

	path := t.TempDir() + "/tone.go"
	_, err := commitTestAttempt(t, s, ctx, st.sessionID, st.resourceID,
		[]CommitFile{{Path: path, Content: "package out\n"}}, "", "claude-sonnet-5")
	require.NoError(t, err)

	files, err := s.store.GetGeneratedFiles(st.resourceID)
	require.NoError(t, err)
	require.Len(t, files, 1)

	// Re-settle with no files (the adopt path).
	_, err = s.store.ReopenSessionResources(
		ctx, st.sessionID, []string{st.resourceID}, "validation batch requested owner repair",
	)
	require.NoError(t, err)
	_, err = commitTestAttempt(t, s, ctx, st.sessionID, st.resourceID, nil, "adopted", "claude-sonnet-5")
	require.NoError(t, err)

	files, err = s.store.GetGeneratedFiles(st.resourceID)
	require.NoError(t, err)
	require.Len(t, files, 1, "a no-file commit must not sever the resource from its files")
	relativePath, err := filepath.Rel(filepath.Dir(s.cfg.SpecDir), path)
	require.NoError(t, err)
	require.Equal(t, filepath.ToSlash(relativePath), files[0].Path)
}

func TestContextWithoutFailureHasNoErrorSection(t *testing.T) {
	s, st := newTestSpecWithSession(t)

	res, err := s.Context(context.Background(), st.sessionID, st.resourceID)
	require.NoError(t, err)

	require.False(t, strings.Contains(res.Prompt, "## Previous Errors"),
		"a clean first attempt must not carry an error section")
}
