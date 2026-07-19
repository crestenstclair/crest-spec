package spec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

// An affected-closure failure that exceeds its repair budget is classified on
// the accepted source, but deliberately has no handoff because no further model
// work is authorized. If triage later proves that diagnostic was attributed to
// an innocent owner, resolving both the classification and resource must
// reinstate the exact accepted bytes after restart. Until that explicit
// resolution, exhaustion remains a hard dispatch boundary.
func TestRestartReinstatesResolvedHandofflessCandidateAfterMisattributedExhaustion(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	specDir := filepath.Join(root, "spec")
	stateDir := filepath.Join(root, ".crest-spec")
	require.NoError(t, os.MkdirAll(specDir, 0o755))
	require.NoError(t, os.MkdirAll(stateDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(specDir, "spec.cue"), []byte(withTestIntent(commitTestSpecCUE)), 0o644,
	))

	statePath := filepath.Join(stateDir, "state.db")
	cfg := &config.Config{
		SpecDir: specDir, GenerateModel: "test-model", MaxRetries: 3, WaveMaxRetries: 1,
		TypeCheckCommand: "true",
	}
	open := func() (*Spec, *store.Store) {
		t.Helper()
		st, err := store.New(statePath)
		require.NoError(t, err)
		return New(st, OSFileSystem{}, cfg), st
	}

	initial, initialStore := open()
	begin, err := initial.Begin(ctx, BeginOpts{})
	require.NoError(t, err)
	require.Len(t, begin.Plan, 1)
	resourceID := begin.Plan[0].ResourceID
	path := "generated/tone.go"
	first, err := commitTestAttempt(t, initial, ctx, begin.SessionID, resourceID, []CommitFile{{
		Path: path, Content: "package generated\n\nconst Tone = 440\n",
	}}, "initial accepted candidate", "test-model")
	require.NoError(t, err)
	require.True(t, first.Committed)

	firstRoute, err := initialStore.RouteClosureValidationFailures(ctx, store.ClosureValidationFailureRoutingInput{
		SessionID: begin.SessionID, ResourceIDs: []string{resourceID},
		Reason: "first affected-closure repair", Category: execution.FailureIntegrationError,
		MaxRepairRetries: 1,
	})
	require.NoError(t, err)
	require.Equal(t, []string{resourceID}, firstRoute.ReopenedResources)
	require.Len(t, firstRoute.Handoffs, 1)
	require.Empty(t, firstRoute.ExhaustedResources)

	accepted, err := commitTestAttempt(t, initial, ctx, begin.SessionID, resourceID, []CommitFile{{
		Path: path, Content: "package generated\n\nconst Tone = 441\n",
	}}, "accepted closure repair", "test-model")
	require.NoError(t, err)
	require.True(t, accepted.Committed)

	exhausted, err := initialStore.RouteClosureValidationFailures(ctx, store.ClosureValidationFailureRoutingInput{
		SessionID: begin.SessionID, ResourceIDs: []string{resourceID},
		Reason:   "diagnostic later proven to belong to another owner",
		Category: execution.FailureIntegrationError, MaxRepairRetries: 1,
	})
	require.NoError(t, err)
	require.Empty(t, exhausted.ReopenedResources)
	require.Equal(t, []string{resourceID}, exhausted.ExhaustedResources)
	require.Len(t, exhausted.Failures, 1)
	require.Empty(t, exhausted.Handoffs,
		"retry exhaustion must not manufacture unauthorized repair work")
	exhaustedFailure := exhausted.Failures[0]
	require.Equal(t, accepted.AttemptID, exhaustedFailure.AttemptID)

	resource, err := initialStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateErrored), resource.State)
	require.Contains(t, resource.LastError, "affected-closure repair retries exhausted")
	require.Equal(t, exhaustedFailure.ID, resource.ExhaustionFailureID,
		"the exhausted projection must atomically link its exact current failure")
	handoffs, err := initialStore.ListAttemptHandoffsBySource(ctx, accepted.AttemptID)
	require.NoError(t, err)
	require.Empty(t, handoffs,
		"the exhausted classification must be durably handoffless")
	attemptsBefore, err := initialStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.NoError(t, initialStore.Close())

	restarted, restartedStore := open()
	t.Cleanup(func() { require.NoError(t, restartedStore.Close()) })

	// Restart alone cannot turn a real unresolved exhaustion into work or
	// silently accept it. It remains visible and context creation fails before
	// another immutable attempt is created.
	next, err := restarted.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, next.Resources, 1)
	require.Equal(t, StateErrored, next.Resources[0].State)
	_, err = restarted.Context(ctx, begin.SessionID, resourceID)
	require.ErrorContains(t, err, "exhausted affected-closure repair retries")
	attemptsWhileUnresolved, err := restartedStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, attemptsWhileUnresolved, len(attemptsBefore))

	const triageGuidance = "triage proved this accepted owner was innocent; preserve its exact candidate"
	require.NoError(t, restarted.Resolve(
		ctx, begin.SessionID, resourceID, triageGuidance, "",
	))
	resolvedFailure, err := restartedStore.GetFailureClassification(ctx, exhaustedFailure.ID)
	require.NoError(t, err)
	require.NotNil(t, resolvedFailure.ResolvedAt,
		"one spec/resolve action must atomically resolve the linked exhaustion failure")
	require.Equal(t, triageGuidance, resolvedFailure.Resolution)
	require.Empty(t, resolvedFailure.ResolvedByAttempt)

	reinstatement, err := restarted.Context(ctx, begin.SessionID, resourceID)
	require.NoError(t, err)
	require.True(t, reinstatement.NoDispatch)
	require.Equal(t, accepted.CandidateID, reinstatement.ReinstatedCandidateID)
	require.Empty(t, reinstatement.AttemptID)
	require.Empty(t, reinstatement.HandoffID)

	reinstated, err := restartedStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), reinstated.State)
	require.Equal(t, "integration_pending", reinstated.Phase)
	require.Empty(t, reinstated.LastError)
	require.Empty(t, reinstated.ExhaustionFailureID,
		"successful reinstatement must consume the projected exhaustion link")
	attemptsAfter, err := restartedStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfter, len(attemptsBefore),
		"reinstating exact accepted provenance must not consume model work")

	bytes, err := os.ReadFile(filepath.Join(root, path))
	require.NoError(t, err)
	require.Equal(t, "package generated\n\nconst Tone = 441\n", string(bytes))
	candidate, err := restartedStore.GetCandidateSetByAttempt(ctx, accepted.AttemptID)
	require.NoError(t, err)
	require.Equal(t, accepted.CandidateID, candidate.ID)
	require.Equal(t, "accepted", candidate.Status)

	// Reinstatement is not success by itself. The same restarted session must
	// reach and pass its real affected-closure gate before the candidate is
	// considered recovered.
	nextAfterReinstatement, err := restarted.Next(ctx, begin.SessionID)
	require.NoError(t, err)
	require.True(t, nextAfterReinstatement.ValidationReady)
	require.Empty(t, nextAfterReinstatement.Resources)
	closure := restarted.VerifyWave(
		ctx, begin.SessionID, nextAfterReinstatement.ValidationWaveIndex,
	)
	require.True(t, closure.Passed)
	require.True(t, closure.IntegrationValidated)
	afterClosure, err := restartedStore.GetSessionResource(begin.SessionID, resourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), afterClosure.State)
	require.Empty(t, afterClosure.ExhaustionFailureID)
	afterClosureAttempts, err := restartedStore.ListGenerationAttemptsBySession(ctx, begin.SessionID)
	require.NoError(t, err)
	require.Len(t, afterClosureAttempts, len(attemptsBefore),
		"the closure gate after reinstatement must not manufacture model work")
	afterClosureHandoffs, err := restartedStore.ListAttemptHandoffsBySource(ctx, accepted.AttemptID)
	require.NoError(t, err)
	require.Empty(t, afterClosureHandoffs,
		"the handoffless exhausted path must remain handoffless after closure validation")
	afterClosureBytes, err := os.ReadFile(filepath.Join(root, path))
	require.NoError(t, err)
	require.Equal(t, "package generated\n\nconst Tone = 441\n", string(afterClosureBytes),
		"closure validation must preserve the exact reinstated candidate bytes")
}
