package store

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/crestenstclair/crest-spec/internal/execution"
	migrationspkg "github.com/crestenstclair/crest-spec/migrations"
)

const exhaustionProvenanceResourceID = "resource.closure-routing"

func TestExhaustedClosureRoutingAtomicallyLinksExactFailureAcrossRestart(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "state.db")
	st := newAcceptedClosureRoutingStore(t, databasePath)
	seedResolvedAffectedClosureCycle(t, st, "attempt-1")

	result, err := st.routeClosureValidationFailures(ctx, ClosureValidationFailureRoutingInput{
		SessionID: "session-1", ResourceIDs: []string{exhaustionProvenanceResourceID},
		Reason: "second closure failure", Category: execution.FailureIntegrationError,
		MaxRepairRetries: 1,
		transitionHook: func(stage, _ string) error {
			if stage == "after_exhausted" {
				return errTestExhaustionInterruption{}
			}
			return nil
		},
	})
	require.ErrorContains(t, err, "simulated exhaustion interruption")
	require.Nil(t, result)

	rolledBack, err := st.GetSessionResource("session-1", exhaustionProvenanceResourceID)
	require.NoError(t, err)
	require.Equal(t, "committed", rolledBack.State)
	require.Empty(t, rolledBack.ExhaustionFailureID,
		"the resource must never expose an exhaustion link without its failure commit")
	failures, err := st.ListFailureClassificationsByAttempt(ctx, "attempt-1")
	require.NoError(t, err)
	require.Len(t, failures, 1,
		"the failure and exhaustion projection must roll back in the same transaction")

	result, err = st.RouteClosureValidationFailures(ctx, ClosureValidationFailureRoutingInput{
		SessionID: "session-1", ResourceIDs: []string{exhaustionProvenanceResourceID},
		Reason: "second closure failure", Category: execution.FailureIntegrationError,
		MaxRepairRetries: 1,
	})
	require.NoError(t, err)
	require.Empty(t, result.ReopenedResources)
	require.Equal(t, []string{exhaustionProvenanceResourceID}, result.ExhaustedResources)
	require.Len(t, result.Failures, 1)
	require.Empty(t, result.Handoffs)
	exhaustedFailure := result.Failures[0]
	require.Equal(t, "attempt-1", exhaustedFailure.AttemptID)

	linked, err := st.GetSessionResource("session-1", exhaustionProvenanceResourceID)
	require.NoError(t, err)
	require.Equal(t, exhaustedFailure.ID, linked.ExhaustionFailureID)
	require.Equal(t, "errored", linked.State)
	require.Equal(t, "integration_failed", linked.Phase)

	require.NoError(t, st.Close())
	restarted, err := New(databasePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Close()) })
	durable, err := restarted.GetSessionResource("session-1", exhaustionProvenanceResourceID)
	require.NoError(t, err)
	require.Equal(t, exhaustedFailure.ID, durable.ExhaustionFailureID)
	durableFailure, err := restarted.GetFailureClassification(ctx, durable.ExhaustionFailureID)
	require.NoError(t, err)
	require.Equal(t, exhaustedFailure.ID, durableFailure.ID)
	require.Equal(t, "attempt-1", durableFailure.AttemptID)
	require.Nil(t, durableFailure.ResolvedAt)
}

type errTestExhaustionInterruption struct{}

func (errTestExhaustionInterruption) Error() string {
	return "simulated exhaustion interruption"
}

func TestHandofflessNonExhaustedIntegrationFailureCannotReinstate(t *testing.T) {
	ctx := context.Background()
	st := newAcceptedClosureRoutingStore(t, filepath.Join(t.TempDir(), "state.db"))
	failure, err := st.CreateFailureClassification(ctx, FailureClassificationWrite{
		AttemptID: "attempt-1", Category: execution.FailureIntegrationError,
		Origin: "engine", Confidence: 1, EvidenceSource: "affected_closure_validation",
		Evidence: "an unlinked handoffless failure",
	})
	require.NoError(t, err)
	const resolution = "triage resolved the unlinked diagnostic"
	require.NoError(t, st.ResolveFailureClassification(ctx, failure.ID, resolution, ""))
	setRecoverableExhaustionProjection(t, st)

	reinstated, err := st.ReinstateAcceptedSessionResource(
		ctx, acceptedReinstatementInput(t, st, failure, resolution),
	)
	require.ErrorContains(t, err, "durable eligibility changed")
	require.False(t, reinstated)
	resource, err := st.GetSessionResource("session-1", exhaustionProvenanceResourceID)
	require.NoError(t, err)
	require.Equal(t, "pending", resource.State)
	require.Empty(t, resource.ExhaustionFailureID,
		"a handoffless integration error is eligible only when routing linked exact exhaustion provenance")
}

func TestResolvedLinkedFailureWithNewerUnresolvedClosureFailureCannotResolveOrReinstate(t *testing.T) {
	ctx := context.Background()
	st, linkedFailure := newExhaustedProvenanceStore(t)
	competitor, err := st.CreateFailureClassification(ctx, FailureClassificationWrite{
		AttemptID: "attempt-1", Category: execution.FailureIntegrationError,
		Origin: "engine", Confidence: 1, EvidenceSource: "affected_closure_validation",
		Evidence: "newer unresolved closure diagnostic",
	})
	require.NoError(t, err)
	const resolution = "the linked older diagnostic was misattributed"
	require.NoError(t, st.ResolveFailureClassification(ctx, linkedFailure.ID, resolution, ""))

	err = st.ResolveSessionResource(
		ctx, "session-1", exhaustionProvenanceResourceID, "preserve the accepted candidate",
	)
	require.ErrorContains(t, err, "unresolved_competitors=1")
	resource, err := st.GetSessionResource("session-1", exhaustionProvenanceResourceID)
	require.NoError(t, err)
	require.Equal(t, "errored", resource.State,
		"ambiguous resolution must not partially clear the resource projection")
	require.Equal(t, linkedFailure.ID, resource.ExhaustionFailureID)
	stillUnresolved, err := st.GetFailureClassification(ctx, competitor.ID)
	require.NoError(t, err)
	require.Nil(t, stillUnresolved.ResolvedAt)

	setRecoverableExhaustionProjection(t, st)
	reinstated, err := st.ReinstateAcceptedSessionResource(
		ctx, acceptedReinstatementInput(t, st, linkedFailure, resolution),
	)
	require.ErrorContains(t, err, "unresolved_competitors=1")
	require.False(t, reinstated)
}

func TestUnsafeNewerExecutedCandidateAttemptCannotReinstateExhaustedSource(t *testing.T) {
	ctx := context.Background()
	st, linkedFailure := newExhaustedProvenanceStore(t)
	setRecoverableExhaustionProjection(t, st)

	prepareAndClaimTestContext(
		t, st, testContextManifest("attempt-2", "manifest-2", exhaustionProvenanceResourceID),
	)
	manifest := testExecutionManifest()
	manifest.AttemptID = "attempt-2"
	manifest.ContextManifestID = "manifest-2"
	manifest.IdempotencyKey = "host-request-2"
	started, err := st.StartExecution(ctx, manifest)
	require.NoError(t, err)
	_, err = st.SubmitCandidate(ctx, started.ID, []CandidateFile{{
		Path: "unsafe.go", Content: "package unsafe\n", WriteIntent: "create",
	}})
	require.NoError(t, err)

	const resolution = "the exhausted source diagnostic was misattributed"
	require.NoError(t, st.ResolveFailureClassification(ctx, linkedFailure.ID, resolution, ""))
	setRecoverableExhaustionProjection(t, st)
	reinstated, err := st.ReinstateAcceptedSessionResource(
		ctx, acceptedReinstatementInput(t, st, linkedFailure, resolution),
	)
	require.ErrorContains(t, err, "unsafe_newer=1")
	require.False(t, reinstated,
		"an executed or candidate-bearing newer attempt is unsafe even if the old candidate still matches")
}

func TestMigration032BackfillsOnlyUnambiguousStrictlyExhaustedLegacyProvenance(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "legacy-state.db")
	database := openLegacyDatabaseBeforeMigration032(t, databasePath)
	insertLegacyExhaustionBase(t, database)

	insertLegacyAcceptedAttempt(t, database, "eligible", 1, "")
	insertLegacyFailure(t, database, "eligible-history", "eligible-attempt-1", true)
	insertLegacyFailure(t, database, "eligible-current", "eligible-attempt-1", false)
	insertLegacySessionResource(t, database, "eligible", 1, 1)

	insertLegacyAcceptedAttempt(t, database, "boundary", 1, "")
	insertLegacyFailure(t, database, "boundary-history", "boundary-attempt-1", true)
	insertLegacyFailure(t, database, "boundary-current", "boundary-attempt-1", false)
	insertLegacySessionResource(t, database, "boundary", 2, 2)

	insertLegacyAcceptedAttempt(t, database, "handoff", 1, "")
	insertLegacyFailure(t, database, "handoff-history", "handoff-attempt-1", true)
	insertLegacyFailure(t, database, "handoff-current", "handoff-attempt-1", false)
	insertLegacyHandoff(t, database, "handoff-current", "handoff-attempt-1")
	insertLegacySessionResource(t, database, "handoff", 1, 1)

	insertLegacyAcceptedAttempt(t, database, "unsafe", 1, "")
	insertLegacyFailure(t, database, "unsafe-history", "unsafe-attempt-1", true)
	insertLegacyFailure(t, database, "unsafe-current", "unsafe-attempt-1", false)
	insertLegacyAcceptedAttempt(t, database, "unsafe", 2, "unsafe-attempt-1")
	_, err := database.Exec(`
		UPDATE generation_attempts SET status = 'abandoned' WHERE id = 'unsafe-attempt-2';
		UPDATE execution_manifests SET status = 'cancelled' WHERE attempt_id = 'unsafe-attempt-2';
		UPDATE candidate_sets SET status = 'submitted', disposed_at = NULL
		WHERE attempt_id = 'unsafe-attempt-2'`)
	require.NoError(t, err)
	insertLegacySessionResource(t, database, "unsafe", 1, 1)

	insertLegacyAcceptedAttempt(t, database, "ambiguous", 1, "")
	insertLegacyFailure(t, database, "ambiguous-history", "ambiguous-attempt-1", true)
	insertLegacyFailure(t, database, "ambiguous-linked", "ambiguous-attempt-1", false)
	insertLegacyFailure(t, database, "ambiguous-newer", "ambiguous-attempt-1", false)
	_, err = database.Exec(`
		UPDATE failure_classifications SET created_at = '2026-01-01T00:00:02Z'
		WHERE id = 'ambiguous-newer'`)
	require.NoError(t, err)
	insertLegacySessionResource(t, database, "ambiguous", 1, 1)

	require.NoError(t, database.Close())
	migrated, err := New(databasePath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, migrated.Close()) })

	eligible, err := migrated.GetSessionResource("legacy-session", "eligible")
	require.NoError(t, err)
	require.Equal(t, "eligible-current", eligible.ExhaustionFailureID,
		"the exact latest accepted candidate failure is the only provenance safe to backfill")
	for _, resourceID := range []string{"boundary", "handoff", "unsafe", "ambiguous"} {
		resource, getErr := migrated.GetSessionResource("legacy-session", resourceID)
		require.NoError(t, getErr)
		require.Empty(t, resource.ExhaustionFailureID, resourceID)
	}

	var applied int
	require.NoError(t, migrated.sqlDB.QueryRow(`
		SELECT COUNT(*) FROM schema_migrations
		WHERE filename = '032_affected_closure_exhaustion_provenance.sql'`,
	).Scan(&applied))
	require.Equal(t, 1, applied)
}

func seedResolvedAffectedClosureCycle(t *testing.T, st *Store, attemptID string) {
	t.Helper()
	ctx := context.Background()
	failure, err := st.CreateFailureClassification(ctx, FailureClassificationWrite{
		AttemptID: attemptID, Category: execution.FailureIntegrationError,
		Origin: "engine", Confidence: 1, EvidenceSource: "affected_closure_validation",
		Evidence: "earlier repair cycle",
	})
	require.NoError(t, err)
	require.NoError(t, st.ResolveFailureClassification(
		ctx, failure.ID, "resolved by an accepted repair", attemptID,
	))
}

func newExhaustedProvenanceStore(t *testing.T) (*Store, *FailureClassification) {
	t.Helper()
	st := newAcceptedClosureRoutingStore(t, filepath.Join(t.TempDir(), "state.db"))
	seedResolvedAffectedClosureCycle(t, st, "attempt-1")
	result, err := st.RouteClosureValidationFailures(context.Background(), ClosureValidationFailureRoutingInput{
		SessionID: "session-1", ResourceIDs: []string{exhaustionProvenanceResourceID},
		Reason: "closure retry budget exhausted", Category: execution.FailureIntegrationError,
		MaxRepairRetries: 1,
	})
	require.NoError(t, err)
	require.Len(t, result.Failures, 1)
	require.Equal(t, []string{exhaustionProvenanceResourceID}, result.ExhaustedResources)
	return st, &result.Failures[0]
}

func setRecoverableExhaustionProjection(t *testing.T, st *Store) {
	t.Helper()
	_, err := st.sqlDB.Exec(`
		UPDATE session_resources
		SET state = 'pending', phase = 'integration_failed', last_error = NULL,
		    last_output = NULL, job_id = NULL
		WHERE session_id = 'session-1' AND resource_id = ?`, exhaustionProvenanceResourceID)
	require.NoError(t, err)
}

func acceptedReinstatementInput(
	t *testing.T,
	st *Store,
	failure *FailureClassification,
	resolution string,
) AcceptedCandidateReinstatement {
	t.Helper()
	candidate, err := st.GetCandidateSetByAttempt(context.Background(), "attempt-1")
	require.NoError(t, err)
	return AcceptedCandidateReinstatement{
		SessionID: "session-1", ResourceID: exhaustionProvenanceResourceID,
		SourceAttemptID: "attempt-1", PlanOperationID: "operation-1",
		CandidateID: candidate.ID, CandidateHash: candidate.CandidateHash,
		GenerationInputHash:    candidate.GenerationInputHash,
		DependencyContractHash: candidate.DependencyContractHash,
		FailureID:              failure.ID, FailureCategory: failure.Category, Reason: resolution,
	}
}

func openLegacyDatabaseBeforeMigration032(t *testing.T, databasePath string) *sql.DB {
	t.Helper()
	database, err := openSQLite(databasePath)
	require.NoError(t, err)
	_, err = database.Exec(`CREATE TABLE schema_migrations (filename TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	entries, err := fs.ReadDir(migrationspkg.FS, ".")
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") &&
			entry.Name() < "032_affected_closure_exhaustion_provenance.sql" {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		contents, readErr := fs.ReadFile(migrationspkg.FS, name)
		require.NoError(t, readErr)
		tx, beginErr := database.Begin()
		require.NoError(t, beginErr)
		_, execErr := tx.Exec(string(contents))
		require.NoError(t, execErr, name)
		_, execErr = tx.Exec(`INSERT INTO schema_migrations (filename) VALUES (?)`, name)
		require.NoError(t, execErr, name)
		require.NoError(t, tx.Commit(), name)
	}
	return database
}

func insertLegacyExhaustionBase(t *testing.T, database *sql.DB) {
	t.Helper()
	const timestamp = "2026-01-01T00:00:00Z"
	_, err := database.Exec(`
		INSERT INTO applies (id, status, spec_hash, started_at)
		VALUES ('legacy-apply', 'running', 'legacy-spec', ?)`, timestamp)
	require.NoError(t, err)
	_, err = database.Exec(`
		INSERT INTO agent_sessions
		    (id, plan_json, waves_json, hashes_json, current_wave, status,
		     created_at, updated_at, apply_id)
		VALUES ('legacy-session', '{}', '[]', '{}', 0, 'active', ?, ?, 'legacy-apply')`,
		timestamp, timestamp)
	require.NoError(t, err)
	_, err = database.Exec(`
		INSERT INTO content_blobs (hash, content, byte_size, created_at)
		VALUES ('legacy-blob', 'legacy prompt', 13, ?)`, timestamp)
	require.NoError(t, err)
}

func insertLegacySessionResource(
	t *testing.T,
	database *sql.DB,
	resourceID string,
	maxRetries, attempts int,
) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO session_resources
		    (session_id, resource_id, state, wave_index, attempts, max_retries,
		     last_error, last_output, job_id, updated_at, phase, dispatched_at,
		     execution_strategy, source_candidate_id)
		VALUES ('legacy-session', ?, 'pending', 0, ?, ?, NULL, NULL, NULL,
		        '2026-01-01T00:00:00Z', 'integration_failed', '', 'generate', NULL)`,
		resourceID, attempts, maxRetries,
	)
	require.NoError(t, err)
}

func insertLegacyAcceptedAttempt(
	t *testing.T,
	database *sql.DB,
	resourceID string,
	retryNumber int,
	parentAttemptID string,
) {
	t.Helper()
	attemptID := resourceID + "-attempt-" + string(rune('0'+retryNumber))
	manifestID := resourceID + "-manifest-" + string(rune('0'+retryNumber))
	executionID := resourceID + "-execution-" + string(rune('0'+retryNumber))
	candidateID := resourceID + "-candidate-" + string(rune('0'+retryNumber))
	var parent any
	if parentAttemptID != "" {
		parent = parentAttemptID
	}
	tx, err := database.Begin()
	require.NoError(t, err)
	_, err = tx.Exec(`
		INSERT INTO generation_attempts
		    (id, session_id, apply_id, resource_id, plan_operation_id,
		     parent_attempt_id, retry_number, role, status, created_at, role_policy_version)
		VALUES (?, 'legacy-session', 'legacy-apply', ?, 'legacy-operation', ?, ?,
		        'resource_implementer', 'accepted', '2026-01-01T00:00:00Z', 'role-policy-v1')`,
		attemptID, resourceID, parent, retryNumber)
	require.NoError(t, err)
	_, err = tx.Exec(`
		INSERT INTO context_manifests
		    (id, attempt_id, selector_version, estimator_version, selection_strategy,
		     budget_tokens, estimated_tokens, original_bytes, selected_bytes,
		     template_hashes_json, system_prompt_blob, rendered_prompt_blob,
		     context_hash, blocked, blocked_reason, created_at)
		VALUES (?, ?, 'selector-v1', 'estimator-v1', 'complete', 1, 0, 0, 0,
		        '{}', 'legacy-blob', 'legacy-blob', ?, 0, '', '2026-01-01T00:00:00Z')`,
		manifestID, attemptID, resourceID+"-context")
	require.NoError(t, err)
	_, err = tx.Exec(`
		INSERT INTO execution_manifests
		    (id, attempt_id, context_manifest_id, protocol_version, idempotency_key,
		     plan_operation_id, role, role_policy_version, context_policy, host_name,
		     model, inference_config_json, inference_config_hash, agent_config_json,
		     agent_config_hash, tool_permissions_json, tool_permissions_hash,
		     template_hashes_json, template_hashes_hash, system_instructions_blob,
		     context_hash, status, started_at, created_at, updated_at)
		VALUES (?, ?, ?, 'protocol-v1', ?, 'legacy-operation', 'resource_implementer',
		        'role-policy-v1', 'legacy-context', 'legacy-host', 'legacy-model',
		        '{}', 'inference-hash', '{}', 'agent-hash', '[]', 'tool-hash',
		        '{}', 'template-hash', 'legacy-blob', ?, 'accepted',
		        '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		executionID, attemptID, manifestID, resourceID+"-request-"+string(rune('0'+retryNumber)),
		resourceID+"-context")
	require.NoError(t, err)
	_, err = tx.Exec(`
		INSERT INTO candidate_sets
		    (id, execution_id, attempt_id, candidate_hash, status, disposition_reason,
		     created_at, disposed_at, generation_input_hash, dependency_contract_hash)
		VALUES (?, ?, ?, ?, 'accepted', 'legacy accepted candidate',
		        '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'generation', 'dependency')`,
		candidateID, executionID, attemptID, resourceID+"-hash-"+string(rune('0'+retryNumber)),
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
}

func insertLegacyFailure(
	t *testing.T,
	database *sql.DB,
	failureID, attemptID string,
	resolved bool,
) {
	t.Helper()
	resolvedAt := any(nil)
	resolvedByAttempt := any(nil)
	resolution := ""
	createdAt := "2026-01-01T00:00:01Z"
	if resolved {
		createdAt = "2026-01-01T00:00:00Z"
		resolvedAt = "2026-01-01T00:00:01Z"
		resolvedByAttempt = attemptID
		resolution = "resolved by accepted repair"
	}
	_, err := database.Exec(`
		INSERT INTO failure_classifications
		    (id, attempt_id, execution_id, resource_id, category, origin, confidence,
		     evidence_source, evidence_reference, corrective_action, next_role,
		     blocks_retry, resolution, resolved_by_attempt, created_at, resolved_at)
		SELECT ?, ?, execution.id, attempt.resource_id, 'integration_error', 'engine', 1,
		       'affected_closure_validation', 'legacy-session', 'repair integration',
		       'integration_implementer', 0, ?, ?, ?, ?
		FROM generation_attempts AS attempt
		JOIN execution_manifests AS execution ON execution.attempt_id = attempt.id
		WHERE attempt.id = ?`,
		failureID, attemptID, resolution, resolvedByAttempt, createdAt, resolvedAt, attemptID,
	)
	require.NoError(t, err)
}

func insertLegacyHandoff(
	t *testing.T,
	database *sql.DB,
	failureID, attemptID string,
) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO attempt_handoffs
		    (id, source_attempt_id, source_role, target_role, reason,
		     expected_outcome, failure_id, status, created_at)
		VALUES (?, ?, 'resource_implementer', 'integration_implementer',
		        'legacy handoff', 'repair diagnostic', ?, 'pending',
		        '2026-01-01T00:00:00Z')`,
		failureID+"-handoff", attemptID, failureID,
	)
	require.NoError(t, err)
}
