package spec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
	"github.com/crestenstclair/crest-spec/internal/store"
)

const crossOwnerReinstatementSpecCUE = `package crestsynth

project: name: "rt"
project: meta: language: "go"
project: contexts: Audio: purpose: "audio"
project: contexts: Audio: valueObjects: InnocentOwner: {
	from: "source"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "provides the source under test"}]
}
project: contexts: Audio: valueObjects: ActualRepairOwner: {
	from: "source"
	contributesTo: [{capability: "capability.exercise_resource", contribution: "provides the repair under test"}]
}
project: invariants: [{text: "no global state"}]
`

const (
	crossOwnerSourceID   = "valueObject.Audio.InnocentOwner"
	crossOwnerResolverID = "valueObject.Audio.ActualRepairOwner"
)

type crossOwnerReinstatementFixture struct {
	spec           *Spec
	sessionID      string
	sourceCommit   *CommitResult
	resolverCommit *CommitResult
	failure        store.FailureClassification
	handoff        store.AttemptHandoff
}

func newCrossOwnerReinstatementFixture(t *testing.T) crossOwnerReinstatementFixture {
	t.Helper()
	ctx := context.Background()
	s, state := newTestSpecWithSessionCUE(t, crossOwnerReinstatementSpecCUE)
	sourceCommit, err := commitTestAttempt(
		t, s, ctx, state.sessionID, crossOwnerSourceID,
		[]CommitFile{{Path: "generated/innocent_owner.go", Content: "package generated\n\nconst InnocentOwner = true\n"}},
		"", "test-model",
	)
	require.NoError(t, err)
	resolverCommit, err := commitTestAttempt(
		t, s, ctx, state.sessionID, crossOwnerResolverID,
		[]CommitFile{{Path: "generated/actual_repair_owner.go", Content: "package generated\n\nconst ActualRepairOwner = true\n"}},
		"", "test-model",
	)
	require.NoError(t, err)

	routed, err := s.store.RouteClosureValidationFailures(ctx, store.ClosureValidationFailureRoutingInput{
		SessionID: state.sessionID, ResourceIDs: []string{crossOwnerSourceID},
		Reason:   "closure diagnostic was initially attributed to the innocent owner",
		Category: execution.FailureIntegrationError, MaxRepairRetries: 3,
	})
	require.NoError(t, err)
	require.Equal(t, []string{crossOwnerSourceID}, routed.ReopenedResources)
	require.Len(t, routed.Failures, 1)
	require.Len(t, routed.Handoffs, 1)
	require.Equal(t, sourceCommit.AttemptID, routed.Failures[0].AttemptID)
	require.Equal(t, sourceCommit.AttemptID, routed.Handoffs[0].SourceAttemptID)

	return crossOwnerReinstatementFixture{
		spec: s, sessionID: state.sessionID, sourceCommit: sourceCommit,
		resolverCommit: resolverCommit, failure: routed.Failures[0], handoff: routed.Handoffs[0],
	}
}

func resolveCrossOwnerFailure(t *testing.T, fixture crossOwnerReinstatementFixture) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, fixture.spec.ResolveFailure(
		ctx, fixture.failure.ID,
		"the accepted repair on the other owner proves this source diagnostic was misattributed",
		fixture.resolverCommit.AttemptID,
	))
	require.NoError(t, fixture.spec.Resolve(
		ctx, fixture.sessionID, crossOwnerSourceID,
		"preserve the innocent owner's exact accepted candidate", "",
	))
}

func TestCrossOwnerAcceptedResolutionReinstatesPendingHandoffSourceWithoutDispatch(t *testing.T) {
	fixture := newCrossOwnerReinstatementFixture(t)
	resolveCrossOwnerFailure(t, fixture)
	ctx := context.Background()
	attemptsBefore, err := fixture.spec.store.ListGenerationAttemptsBySession(ctx, fixture.sessionID)
	require.NoError(t, err)

	result, err := fixture.spec.Context(ctx, fixture.sessionID, crossOwnerSourceID)
	require.NoError(t, err)
	require.True(t, result.NoDispatch,
		"an accepted different-resource resolver must reinstate the exact innocent source candidate")
	require.Equal(t, fixture.sourceCommit.CandidateID, result.ReinstatedCandidateID)
	require.Empty(t, result.AttemptID)

	attemptsAfter, err := fixture.spec.store.ListGenerationAttemptsBySession(ctx, fixture.sessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfter, len(attemptsBefore),
		"cross-owner provenance recovery must not create model work")
	resource, err := fixture.spec.store.GetSessionResource(fixture.sessionID, crossOwnerSourceID)
	require.NoError(t, err)
	require.Equal(t, string(StateCommitted), resource.State)
	handoffs, err := fixture.spec.store.ListAttemptHandoffsBySource(ctx, fixture.sourceCommit.AttemptID)
	require.NoError(t, err)
	require.Len(t, handoffs, 1)
	require.Equal(t, "cancelled", handoffs[0].Status)
}

func TestCrossOwnerResolutionAtomicallyRetiresAcceptedUnclaimedContextBeforeReinstatement(t *testing.T) {
	fixture := newCrossOwnerReinstatementFixture(t)
	ctx := context.Background()
	orphan, err := fixture.spec.PrepareContext(ctx, ContextOptions{
		SessionID: fixture.sessionID, ResourceID: crossOwnerSourceID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, orphan.AttemptID)
	handoffs, err := fixture.spec.store.ListAttemptHandoffsBySource(ctx, fixture.sourceCommit.AttemptID)
	require.NoError(t, err)
	require.Len(t, handoffs, 1)
	require.Equal(t, "accepted", handoffs[0].Status)
	require.Equal(t, orphan.AttemptID, handoffs[0].TargetAttemptID)

	resolveCrossOwnerFailure(t, fixture)
	attemptsBefore, err := fixture.spec.store.ListGenerationAttemptsBySession(ctx, fixture.sessionID)
	require.NoError(t, err)
	result, err := fixture.spec.Context(ctx, fixture.sessionID, crossOwnerSourceID)
	require.NoError(t, err)
	require.True(t, result.NoDispatch,
		"a context that was prepared but never assigned must be retired instead of dispatched")
	require.Equal(t, fixture.sourceCommit.CandidateID, result.ReinstatedCandidateID)
	require.Empty(t, result.AttemptID)

	orphanManifest, err := fixture.spec.store.GetContextManifestByAttempt(ctx, orphan.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "abandoned", orphanManifest.Attempt.Status)
	_, err = fixture.spec.store.GetExecutionManifestByAttempt(ctx, orphan.AttemptID)
	require.Error(t, err)
	_, err = fixture.spec.store.GetCandidateSetByAttempt(ctx, orphan.AttemptID)
	require.Error(t, err)
	handoffs, err = fixture.spec.store.ListAttemptHandoffsBySource(ctx, fixture.sourceCommit.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "cancelled", handoffs[0].Status)
	attemptsAfter, err := fixture.spec.store.ListGenerationAttemptsBySession(ctx, fixture.sessionID)
	require.NoError(t, err)
	require.Len(t, attemptsAfter, len(attemptsBefore),
		"retiring an unclaimed context must not create a replacement attempt")
}
