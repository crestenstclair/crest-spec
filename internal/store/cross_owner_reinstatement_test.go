package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
)

const (
	crossOwnerStoreResolverResource = "resource.actual-repair-owner"
	crossOwnerStoreResolverAttempt  = "attempt-accepted-resolver"
)

type crossOwnerStoreFixture struct {
	store      *Store
	failure    FailureClassification
	handoff    AttemptHandoff
	input      AcceptedCandidateReinstatement
	resolverID string
}

func newCrossOwnerStoreFixture(t *testing.T) crossOwnerStoreFixture {
	t.Helper()
	ctx := context.Background()
	st := newAcceptedClosureRoutingStore(t, filepath.Join(t.TempDir(), "state.db"))
	createAcceptedStoreAttempt(
		t, st, "session-1", "apply-1", crossOwnerStoreResolverResource,
		crossOwnerStoreResolverAttempt, "manifest-accepted-resolver",
	)
	routed, err := st.RouteClosureValidationFailures(ctx, ClosureValidationFailureRoutingInput{
		SessionID: "session-1", ResourceIDs: []string{exhaustionProvenanceResourceID},
		Reason:   "diagnostic initially attributed to the innocent owner",
		Category: execution.FailureIntegrationError, MaxRepairRetries: 3,
	})
	require.NoError(t, err)
	require.Len(t, routed.Failures, 1)
	require.Len(t, routed.Handoffs, 1)
	failure := routed.Failures[0]
	handoff := routed.Handoffs[0]
	const resolution = "accepted different-resource repair proves the source diagnostic was misattributed"
	require.NoError(t, st.ResolveFailureClassification(
		ctx, failure.ID, resolution, crossOwnerStoreResolverAttempt,
	))
	clearCrossOwnerSourceProjection(t, st)
	candidate, err := st.GetCandidateSetByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	return crossOwnerStoreFixture{
		store: st, failure: failure, handoff: handoff,
		resolverID: crossOwnerStoreResolverAttempt,
		input: AcceptedCandidateReinstatement{
			SessionID: "session-1", ResourceID: exhaustionProvenanceResourceID,
			SourceAttemptID: "attempt-1", PlanOperationID: "operation-1",
			CandidateID: candidate.ID, CandidateHash: candidate.CandidateHash,
			GenerationInputHash:    candidate.GenerationInputHash,
			DependencyContractHash: candidate.DependencyContractHash,
			HandoffID:              handoff.ID, FailureID: failure.ID,
			FailureCategory: failure.Category, Reason: resolution,
			ResolverAttemptID: crossOwnerStoreResolverAttempt,
		},
	}
}

func createAcceptedStoreAttempt(
	t *testing.T,
	st *Store,
	sessionID string,
	applyID string,
	resourceID string,
	attemptID string,
	manifestID string,
) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: sessionID, ResourceID: resourceID, State: "pending",
		ExecutionStrategy: "generate", MaxRetries: 3,
	}))
	manifest := testContextManifest(attemptID, manifestID, resourceID)
	manifest.Attempt.SessionID = sessionID
	manifest.Attempt.ApplyID = applyID
	prepareAndClaimTestContext(t, st, manifest)
	executionManifest := testExecutionManifest()
	executionManifest.AttemptID = attemptID
	executionManifest.ContextManifestID = manifestID
	executionManifest.IdempotencyKey = "request-" + attemptID
	started, err := st.StartExecution(ctx, executionManifest)
	require.NoError(t, err)
	candidate, err := st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: resourceID + ".go", Content: "package accepted\n", WriteIntent: "create",
	}})
	require.NoError(t, err)
	_, err = st.TransitionExecution(ctx, ExecutionTransition{
		ExecutionID: started.ID, ToStatus: execution.StatusValidating,
	})
	require.NoError(t, err)
	_, err = st.FinalizeCandidate(ctx, CandidateDisposition{
		ExecutionID: started.ID, Accepted: true, Reason: "accepted resolver candidate",
		Resource: &Resource{
			ID: resourceID, Kind: "asset", DeclarationHash: "declaration-" + resourceID,
			EffectiveHash:          "effective-" + resourceID,
			GenerationInputHash:    "generation-" + resourceID,
			DependencyContractHash: "dependency-" + resourceID, Model: "test-model",
		},
		Files: []GeneratedFile{{
			Path: resourceID + ".go", ResourceID: resourceID,
			ContentHash: candidate.Files[0].ContentHash,
			PromptHash:  started.ContextHash, Model: "test-model",
		}},
		Attempts: 1,
	})
	require.NoError(t, err)
}

func createPreparedStoreAttempt(
	t *testing.T,
	st *Store,
	sessionID string,
	applyID string,
	resourceID string,
	attemptID string,
) DispatchLifecycle {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, st.UpsertSessionResource(SessionResource{
		SessionID: sessionID, ResourceID: resourceID, State: "pending",
		ExecutionStrategy: "generate", MaxRetries: 3,
	}))
	lifecycle, err := st.RequestContext(ctx, DispatchRequest{
		SessionID: sessionID, ResourceID: resourceID,
	})
	require.NoError(t, err)
	manifest := testContextManifest(attemptID, "manifest-"+attemptID, resourceID)
	manifest.Attempt.SessionID = sessionID
	manifest.Attempt.ApplyID = applyID
	_, err = st.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: manifest, DispatchLifecycleID: lifecycle.ID,
	})
	require.NoError(t, err)
	bound, err := st.GetDispatchLifecycle(ctx, lifecycle.ID)
	require.NoError(t, err)
	return *bound
}

func acceptCrossOwnerRepairHandoffContext(
	t *testing.T,
	fixture crossOwnerStoreFixture,
) DispatchLifecycle {
	t.Helper()
	ctx := context.Background()
	lifecycle, err := fixture.store.RequestContext(ctx, DispatchRequest{
		SessionID: fixture.input.SessionID, ResourceID: fixture.input.ResourceID,
	})
	require.NoError(t, err)
	manifest := testContextManifest(
		"attempt-unclaimed-repair", "manifest-unclaimed-repair", fixture.input.ResourceID,
	)
	manifest.Attempt.Role = fixture.handoff.TargetRole
	_, err = fixture.store.CreateContextManifest(ctx, ContextManifestWrite{
		Manifest: manifest, HandoffID: fixture.handoff.ID, DispatchLifecycleID: lifecycle.ID,
	})
	require.NoError(t, err)
	bound, err := fixture.store.GetDispatchLifecycle(ctx, lifecycle.ID)
	require.NoError(t, err)
	return *bound
}

func clearCrossOwnerSourceProjection(t *testing.T, st *Store) {
	t.Helper()
	_, err := st.sqlDB.Exec(`
		UPDATE session_resources
		SET state = 'pending', phase = 'integration_failed', last_error = NULL,
		    last_output = NULL, job_id = NULL
		WHERE session_id = 'session-1' AND resource_id = ?`, exhaustionProvenanceResourceID)
	require.NoError(t, err)
}

func TestCrossOwnerReinstatementResolverProvenanceFailsClosed(t *testing.T) {
	t.Run("same resource", func(t *testing.T) {
		fixture := newCrossOwnerStoreFixture(t)
		_, err := fixture.store.sqlDB.Exec(`
			UPDATE failure_classifications SET resolved_by_attempt = 'attempt-1'
			WHERE id = ?`, fixture.failure.ID)
		require.NoError(t, err)
		fixture.input.ResolverAttemptID = "attempt-1"
		assertCrossOwnerStoreReinstatementRejected(t, fixture)
	})

	t.Run("missing resolver input", func(t *testing.T) {
		fixture := newCrossOwnerStoreFixture(t)
		fixture.input.ResolverAttemptID = ""
		assertCrossOwnerStoreReinstatementRejected(t, fixture)
	})

	t.Run("missing resolver row", func(t *testing.T) {
		fixture := newCrossOwnerStoreFixture(t)
		ctx := context.Background()
		conn, err := fixture.store.sqlDB.Conn(ctx)
		require.NoError(t, err)
		_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`)
		require.NoError(t, err)
		const missingAttemptID = "attempt-missing-resolver"
		_, err = conn.ExecContext(ctx, `
			UPDATE failure_classifications SET resolved_by_attempt = ? WHERE id = ?`,
			missingAttemptID, fixture.failure.ID,
		)
		require.NoError(t, err)
		_, err = conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
		require.NoError(t, err)
		require.NoError(t, conn.Close())
		fixture.input.ResolverAttemptID = missingAttemptID
		assertCrossOwnerStoreReinstatementRejected(t, fixture)
	})

	t.Run("nonaccepted resolver", func(t *testing.T) {
		fixture := newCrossOwnerStoreFixture(t)
		const attemptID = "attempt-nonaccepted-resolver"
		createPreparedStoreAttempt(
			t, fixture.store, "session-1", "apply-1", "resource.nonaccepted-resolver", attemptID,
		)
		_, err := fixture.store.sqlDB.Exec(`
			UPDATE failure_classifications SET resolved_by_attempt = ? WHERE id = ?`,
			attemptID, fixture.failure.ID,
		)
		require.NoError(t, err)
		fixture.input.ResolverAttemptID = attemptID
		assertCrossOwnerStoreReinstatementRejected(t, fixture)
	})

	t.Run("cross session resolver", func(t *testing.T) {
		fixture := newCrossOwnerStoreFixture(t)
		require.NoError(t, fixture.store.CreateApply("apply-2", "spec-hash-2"))
		require.NoError(t, fixture.store.CreateSession(Session{
			ID: "session-2", ApplyID: "apply-2", PlanJSON: "[]", WavesJSON: "[]", HashesJSON: "{}",
		}))
		const attemptID = "attempt-cross-session-resolver"
		createAcceptedStoreAttempt(
			t, fixture.store, "session-2", "apply-2", "resource.cross-session-resolver",
			attemptID, "manifest-cross-session-resolver",
		)
		_, err := fixture.store.sqlDB.Exec(`
			UPDATE failure_classifications SET resolved_by_attempt = ? WHERE id = ?`,
			attemptID, fixture.failure.ID,
		)
		require.NoError(t, err)
		fixture.input.ResolverAttemptID = attemptID
		assertCrossOwnerStoreReinstatementRejected(t, fixture)
	})

	t.Run("ambiguous failure handoffs", func(t *testing.T) {
		fixture := newCrossOwnerStoreFixture(t)
		_, err := fixture.store.CreateAttemptHandoff(context.Background(), AttemptHandoff{
			SourceAttemptID: fixture.input.SourceAttemptID,
			SourceRole:      fixture.handoff.SourceRole, TargetRole: fixture.handoff.TargetRole,
			Reason: "duplicate ambiguous handoff", ExpectedOutcome: "must fail closed",
			FailureID: fixture.failure.ID,
		})
		require.NoError(t, err)
		assertCrossOwnerStoreReinstatementRejected(t, fixture)
	})
}

func assertCrossOwnerStoreReinstatementRejected(t *testing.T, fixture crossOwnerStoreFixture) {
	t.Helper()
	reinstated, err := fixture.store.ReinstateAcceptedSessionResource(
		context.Background(), fixture.input,
	)
	require.ErrorContains(t, err, "durable eligibility changed")
	require.False(t, reinstated)
	resource, resourceErr := fixture.store.GetSessionResource("session-1", exhaustionProvenanceResourceID)
	require.NoError(t, resourceErr)
	require.Equal(t, "pending", resource.State)
	handoffs, handoffErr := fixture.store.ListAttemptHandoffsBySource(
		context.Background(), fixture.input.SourceAttemptID,
	)
	require.NoError(t, handoffErr)
	for _, handoff := range handoffs {
		require.NotEqual(t, "cancelled", handoff.Status)
	}
}

func TestCrossOwnerReinstatementRejectsAcceptedTargetWithLifecycleOrCandidateEvidence(t *testing.T) {
	t.Run("dispatch requested", func(t *testing.T) {
		fixture := newCrossOwnerStoreFixture(t)
		lifecycle := acceptCrossOwnerRepairHandoffContext(t, fixture)
		_, err := fixture.store.RequestDispatch(context.Background(), DispatchRequest{
			ID: lifecycle.ID, SessionID: fixture.input.SessionID,
			ResourceID:    fixture.input.ResourceID,
			HostSessionID: "host-with-dispatch-evidence", WorkerID: "worker-with-dispatch-evidence",
		})
		require.NoError(t, err)
		clearCrossOwnerSourceProjection(t, fixture.store)
		assertCrossOwnerStoreReinstatementRejected(t, fixture)
		manifest, err := fixture.store.GetContextManifestByAttempt(
			context.Background(), lifecycle.AttemptID,
		)
		require.NoError(t, err)
		require.Equal(t, "context_prepared", manifest.Attempt.Status)
	})

	t.Run("execution and candidate submitted", func(t *testing.T) {
		fixture := newCrossOwnerStoreFixture(t)
		lifecycle := acceptCrossOwnerRepairHandoffContext(t, fixture)
		_, err := fixture.store.RequestDispatch(context.Background(), DispatchRequest{
			ID: lifecycle.ID, SessionID: fixture.input.SessionID,
			ResourceID:    fixture.input.ResourceID,
			HostSessionID: "host-with-candidate-evidence", WorkerID: "worker-with-candidate-evidence",
		})
		require.NoError(t, err)
		_, err = fixture.store.AcknowledgeDispatch(context.Background(), DispatchRequest{
			ID: lifecycle.ID, SessionID: fixture.input.SessionID,
			ResourceID:    fixture.input.ResourceID,
			HostSessionID: "host-with-candidate-evidence", WorkerID: "worker-with-candidate-evidence",
		})
		require.NoError(t, err)
		executionManifest := testExecutionManifest()
		executionManifest.AttemptID = lifecycle.AttemptID
		executionManifest.ContextManifestID = "manifest-unclaimed-repair"
		executionManifest.IdempotencyKey = "request-evidenced-target"
		executionManifest.Role = fixture.handoff.TargetRole
		executionManifest.HostSessionID = "host-with-candidate-evidence"
		targetPolicy, err := execution.LookupRole(fixture.handoff.TargetRole)
		require.NoError(t, err)
		executionManifest.ContextPolicy = targetPolicy.ContextPolicy
		started, err := fixture.store.StartExecution(context.Background(), executionManifest)
		require.NoError(t, err)
		_, err = fixture.store.SubmitCandidate(context.Background(), started.ID, []CandidateFile{{
			Path: "candidate-evidence.go", Content: "package evidence\n", WriteIntent: "create",
		}})
		require.NoError(t, err)
		_, err = fixture.store.sqlDB.Exec(`
			UPDATE generation_attempts SET status = 'abandoned' WHERE id = ?`, lifecycle.AttemptID)
		require.NoError(t, err)
		clearCrossOwnerSourceProjection(t, fixture.store)
		assertCrossOwnerStoreReinstatementRejected(t, fixture)
	})
}

func TestCrossOwnerPreparedContextReinstatementRollsBackEveryProjectionOnAuditFailure(t *testing.T) {
	fixture := newCrossOwnerStoreFixture(t)
	lifecycle := acceptCrossOwnerRepairHandoffContext(t, fixture)
	_, err := fixture.store.sqlDB.Exec(`
		CREATE TRIGGER fail_cross_owner_reinstatement_audit
		BEFORE INSERT ON apply_actions
		WHEN NEW.action = 'reinstate_candidate'
		BEGIN
		    SELECT RAISE(ABORT, 'simulated reinstatement audit failure');
		END`)
	require.NoError(t, err)

	reinstated, err := fixture.store.ReinstateAcceptedSessionResource(
		context.Background(), fixture.input,
	)
	require.ErrorContains(t, err, "simulated reinstatement audit failure")
	require.False(t, reinstated)
	resource, err := fixture.store.GetSessionResource("session-1", exhaustionProvenanceResourceID)
	require.NoError(t, err)
	require.Equal(t, "pending", resource.State)
	require.Equal(t, "integration_failed", resource.Phase)
	handoffs, err := fixture.store.ListAttemptHandoffsBySource(
		context.Background(), fixture.input.SourceAttemptID,
	)
	require.NoError(t, err)
	require.Len(t, handoffs, 1)
	require.Equal(t, "accepted", handoffs[0].Status)
	require.Equal(t, lifecycle.AttemptID, handoffs[0].TargetAttemptID)
	manifest, err := fixture.store.GetContextManifestByAttempt(context.Background(), lifecycle.AttemptID)
	require.NoError(t, err)
	require.Equal(t, "context_prepared", manifest.Attempt.Status)
	failures, err := fixture.store.ListFailureClassificationsByAttempt(
		context.Background(), lifecycle.AttemptID,
	)
	require.NoError(t, err)
	require.Empty(t, failures,
		"the retirement audit and attempt transition must roll back with the failed projection")
}

func TestCrossOwnerReinstatementRejectsAnotherUnresolvedClosureFailureWithoutMutation(t *testing.T) {
	fixture := newCrossOwnerStoreFixture(t)
	ctx := context.Background()
	competitor, err := fixture.store.CreateFailureClassification(ctx, FailureClassificationWrite{
		AttemptID: fixture.input.SourceAttemptID,
		Category:  execution.FailureIntegrationError, Origin: "engine", Confidence: 1,
		EvidenceSource: "affected_closure_validation",
		Evidence:       "an independently unresolved closure diagnostic still implicates this owner",
	})
	require.NoError(t, err)
	_, err = fixture.store.sqlDB.Exec(`
		UPDATE failure_classifications SET created_at = '2000-01-01T00:00:00Z'
		WHERE id = ?`, competitor.ID)
	require.NoError(t, err,
		"the competitor is made older so this case isolates the unresolved-competitor guard")

	assertCrossOwnerReinstatementRejectedWithoutMutation(t, fixture)
}

func TestCrossOwnerReinstatementRejectsLaterClosureFailurePastAbandonedUnclaimedTarget(t *testing.T) {
	fixture := newCrossOwnerStoreFixture(t)
	ctx := context.Background()
	laterFailure, err := fixture.store.CreateFailureClassification(ctx, FailureClassificationWrite{
		AttemptID: fixture.input.SourceAttemptID,
		Category:  execution.FailureIntegrationError, Origin: "engine", Confidence: 1,
		EvidenceSource: "affected_closure_validation",
		Evidence:       "a later closure diagnostic supersedes the older resolved handoff",
	})
	require.NoError(t, err)
	_, err = fixture.store.sqlDB.Exec(`
		UPDATE failure_classifications SET created_at = '2999-01-01T00:00:00Z'
		WHERE id = ?`, laterFailure.ID)
	require.NoError(t, err,
		"the diagnostic is made deterministically later so this case isolates the later-failure guard")
	laterHandoff, err := fixture.store.CreateAttemptHandoff(ctx, AttemptHandoff{
		SourceAttemptID: fixture.input.SourceAttemptID,
		SourceRole:      fixture.handoff.SourceRole, TargetRole: fixture.handoff.TargetRole,
		Reason: "repair the later closure diagnostic", ExpectedOutcome: "closure validation passes",
		FailureID: laterFailure.ID,
	})
	require.NoError(t, err)
	laterFixture := fixture
	laterFixture.handoff = *laterHandoff
	laterTarget := acceptCrossOwnerRepairHandoffContext(t, laterFixture)
	recovered, err := fixture.store.AbandonPreparedContext(ctx, PreparedContextAbandonment{
		AttemptID:      laterTarget.AttemptID,
		Reason:         "host retired the unclaimed later repair context before dispatch",
		EvidenceSource: "cross_owner_reinstatement_test",
	})
	require.NoError(t, err)
	require.True(t, recovered.Recovered)
	require.False(t, recovered.ClaimReleased)
	require.NoError(t, fixture.store.ResolveFailureClassification(
		ctx, laterFailure.ID,
		"the later diagnostic was resolved separately by the accepted repair owner",
		fixture.resolverID,
	))
	clearCrossOwnerSourceProjection(t, fixture.store)

	assertCrossOwnerReinstatementRejectedWithoutMutation(t, fixture)
}

func assertCrossOwnerReinstatementRejectedWithoutMutation(
	t *testing.T,
	fixture crossOwnerStoreFixture,
) {
	t.Helper()
	ctx := context.Background()
	resourceBefore, err := fixture.store.GetSessionResource(
		fixture.input.SessionID, fixture.input.ResourceID,
	)
	require.NoError(t, err)
	attemptsBefore, err := fixture.store.ListGenerationAttemptsBySession(ctx, fixture.input.SessionID)
	require.NoError(t, err)
	handoffsBefore, err := fixture.store.ListAttemptHandoffsBySource(ctx, fixture.input.SourceAttemptID)
	require.NoError(t, err)

	reinstated, err := fixture.store.ReinstateAcceptedSessionResource(ctx, fixture.input)
	require.ErrorContains(t, err, "durable eligibility changed")
	require.False(t, reinstated)

	resourceAfter, err := fixture.store.GetSessionResource(
		fixture.input.SessionID, fixture.input.ResourceID,
	)
	require.NoError(t, err)
	attemptsAfter, err := fixture.store.ListGenerationAttemptsBySession(ctx, fixture.input.SessionID)
	require.NoError(t, err)
	handoffsAfter, err := fixture.store.ListAttemptHandoffsBySource(ctx, fixture.input.SourceAttemptID)
	require.NoError(t, err)
	require.Equal(t, resourceBefore, resourceAfter,
		"rejected provenance must not mutate the source resource projection")
	require.Equal(t, attemptsBefore, attemptsAfter,
		"rejected provenance must not retire or create an attempt")
	require.Equal(t, handoffsBefore, handoffsAfter,
		"rejected provenance must not consume any handoff")
}
