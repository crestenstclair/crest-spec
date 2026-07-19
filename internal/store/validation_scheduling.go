package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/crestenstclair/crest-spec/internal/execution"
)

type AcceptedCandidateReinstatement struct {
	SessionID              string
	ResourceID             string
	SourceAttemptID        string
	PlanOperationID        string
	CandidateID            string
	CandidateHash          string
	GenerationInputHash    string
	DependencyContractHash string
	HandoffID              string
	FailureID              string
	FailureCategory        string
	Reason                 string
	// ResolverAttemptID is empty for an explicit human/infrastructure
	// resolution. When populated it must identify one accepted attempt in this
	// session owned by a different resource; that cross-owner provenance is the
	// only non-empty resolved_by_attempt form eligible for reinstatement.
	ResolverAttemptID string
	// SupersededFiles maps a source-candidate path to the exact current
	// workspace hash proven by Spec to come from a later accepted candidate in
	// this same active session. The transaction rechecks that provenance.
	SupersededFiles map[string]string
}

type ValidateExistingReinstatement struct {
	SessionID              string
	ResourceID             string
	PlanOperationID        string
	GenerationInputHash    string
	DependencyContractHash string
	Reason                 string
}

// ReopenSessionResources atomically returns accepted resources implicated by
// an affected-closure validation failure to pending. Accepted candidates,
// files, ownership, and provenance remain intact for minimal-diff repair.
func (s *Store) ReopenSessionResources(ctx context.Context, sessionID string, resourceIDs []string, reason string) (int, error) {
	sessionID = strings.TrimSpace(sessionID)
	reason = strings.TrimSpace(reason)
	if sessionID == "" || reason == "" {
		return 0, fmt.Errorf("reopen session resources: session id and reason are required")
	}
	ids := append([]string(nil), resourceIDs...)
	sort.Strings(ids)
	ids = uniqueStrings(ids)
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("reopen session resources: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	timestamp := now()
	reopened := 0
	for _, resourceID := range ids {
		result, updateErr := tx.ExecContext(ctx, `
			UPDATE session_resources
			SET state = 'pending', phase = 'integration_failed', execution_strategy = 'generate',
			    source_candidate_id = NULL, exhaustion_failure_id = NULL,
			    last_error = ?, attempts = 0,
			    job_id = NULL, updated_at = ?
			WHERE session_id = ? AND resource_id = ? AND state = 'committed'`,
			reason, timestamp, sessionID, resourceID)
		if updateErr != nil {
			return 0, fmt.Errorf("reopen session resources: update %s: %w", resourceID, updateErr)
		}
		count, countErr := result.RowsAffected()
		if countErr != nil {
			return 0, fmt.Errorf("reopen session resources: inspect %s: %w", resourceID, countErr)
		}
		reopened += int(count)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("reopen session resources: commit: %w", err)
	}
	return reopened, nil
}

// ReinstateAcceptedSessionResource closes a validation-repair deadlock without
// model execution. A normal integration repair carries a pending handoff. The
// handoffless recovery paths are also legal only after explicit resolution:
// the legacy invalid-validation bug, and an integration error whose affected-
// closure repair budget was exhausted before routing could create a handoff.
// Both require zero handoffs. Newer attempt history is ignored only when every
// newer attempt was abandoned before execution and candidate submission. Spec
// verifies the candidate against the current plan, ownership, and workspace
// bytes while holding the path coordinator; this transaction atomically
// rechecks durable provenance and its projection.
func (s *Store) ReinstateAcceptedSessionResource(
	ctx context.Context,
	input AcceptedCandidateReinstatement,
) (bool, error) {
	if input.SessionID == "" || input.ResourceID == "" || input.SourceAttemptID == "" ||
		input.PlanOperationID == "" || input.CandidateID == "" || input.CandidateHash == "" ||
		input.GenerationInputHash == "" || input.DependencyContractHash == "" ||
		input.FailureID == "" || input.FailureCategory == "" || strings.TrimSpace(input.Reason) == "" {
		return false, fmt.Errorf("reinstate accepted candidate: complete provenance and reason are required")
	}
	for path, hash := range input.SupersededFiles {
		if strings.TrimSpace(path) == "" || strings.TrimSpace(hash) == "" {
			return false, fmt.Errorf("reinstate accepted candidate: superseded file paths and hashes are required")
		}
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("reinstate accepted candidate: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		applyID, planOperationID, attemptStatus                        string
		sourceRetryNumber                                              int
		candidateHash, generationInputHash, dependencyContractHash     string
		resourceState, phase, lastError, jobID, executionStrategy      string
		exhaustionFailureID                                            string
		candidateStatus, failureCategory, failureOrigin, failureSource string
		failureResolution                                              string
		resolvedAt, resolvedByAttempt                                  string
		blocksRetry, unsafeNewerAttempts, failureHandoffs              int
		otherUnresolvedClosureFailures                                 int
		laterClosureFailures                                           int
	)
	err = tx.QueryRowContext(ctx, `
		SELECT ga.apply_id, ga.plan_operation_id, ga.status, ga.retry_number,
		       cs.candidate_hash, cs.generation_input_hash, cs.dependency_contract_hash,
		       sr.state, sr.phase, COALESCE(sr.last_error, ''), COALESCE(sr.job_id, ''),
		       sr.execution_strategy, COALESCE(sr.exhaustion_failure_id, ''),
		       cs.status, fc.category, fc.origin, fc.evidence_source,
		       fc.resolution, COALESCE(fc.resolved_at, ''), COALESCE(fc.resolved_by_attempt, ''),
		       fc.blocks_retry,
		       (
		           SELECT COUNT(*)
		           FROM generation_attempts AS newer
		           WHERE newer.session_id = ga.session_id
		             AND newer.resource_id = ga.resource_id
		             AND newer.retry_number > ga.retry_number
		             AND (
		                 newer.status != 'abandoned'
		                 OR EXISTS (SELECT 1 FROM execution_manifests AS execution WHERE execution.attempt_id = newer.id)
		                 OR EXISTS (SELECT 1 FROM candidate_sets AS newer_candidate WHERE newer_candidate.attempt_id = newer.id)
		             )
		       ),
		       (SELECT COUNT(*) FROM attempt_handoffs AS failure_handoff WHERE failure_handoff.failure_id = fc.id),
		       (
		           SELECT COUNT(*)
		           FROM failure_classifications AS competing_failure
		           JOIN generation_attempts AS competing_attempt
		             ON competing_attempt.id = competing_failure.attempt_id
		           WHERE competing_attempt.session_id = ga.session_id
		             AND competing_attempt.resource_id = ga.resource_id
		             AND competing_failure.resource_id = ga.resource_id
		             AND competing_failure.category = 'integration_error'
		             AND competing_failure.origin = 'engine'
		             AND competing_failure.evidence_source = 'affected_closure_validation'
		             AND competing_failure.resolved_at IS NULL
		             AND competing_failure.id != fc.id
		       ),
		       (
		           SELECT COUNT(*)
		           FROM failure_classifications AS later_failure
		           JOIN generation_attempts AS later_attempt
		             ON later_attempt.id = later_failure.attempt_id
		           WHERE later_attempt.session_id = ga.session_id
		             AND later_attempt.resource_id = ga.resource_id
		             AND later_failure.resource_id = ga.resource_id
		             AND later_failure.origin = 'engine'
		             AND later_failure.evidence_source = 'affected_closure_validation'
		             AND (
		                 later_failure.created_at > fc.created_at
		                 OR (later_failure.created_at = fc.created_at AND later_failure.id > fc.id)
		             )
		       )
		FROM generation_attempts AS ga
		JOIN candidate_sets AS cs ON cs.attempt_id = ga.id
		JOIN failure_classifications AS fc ON fc.attempt_id = ga.id
		JOIN agent_sessions AS sessions ON sessions.id = ga.session_id
		JOIN session_resources AS sr ON sr.session_id = ga.session_id AND sr.resource_id = ga.resource_id
		WHERE fc.id = ? AND ga.id = ? AND cs.id = ?
		  AND ga.session_id = ? AND ga.resource_id = ?
		  AND fc.resource_id = ga.resource_id
		  AND sessions.status = 'active'`,
		input.FailureID, input.SourceAttemptID, input.CandidateID, input.SessionID, input.ResourceID,
	).Scan(
		&applyID, &planOperationID, &attemptStatus, &sourceRetryNumber,
		&candidateHash, &generationInputHash, &dependencyContractHash,
		&resourceState, &phase, &lastError, &jobID, &executionStrategy,
		&exhaustionFailureID,
		&candidateStatus, &failureCategory, &failureOrigin, &failureSource, &failureResolution,
		&resolvedAt, &resolvedByAttempt, &blocksRetry, &unsafeNewerAttempts, &failureHandoffs,
		&otherUnresolvedClosureFailures, &laterClosureFailures,
	)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reinstate accepted candidate: inspect provenance: %w", err)
	}
	var (
		handoffStatus, handoffTargetAttemptID, handoffTargetStatus         string
		handoffTargetSessionID, handoffTargetResourceID, targetLifecycleID string
		handoffTargetRetryNumber                                           int
		targetExecutions, targetCandidates, targetLifecycleCount           int
		targetDispatchActivity                                             int
	)
	if input.HandoffID != "" {
		var handoffFailureID, handoffSourceAttemptID string
		if err := tx.QueryRowContext(ctx, `
			SELECT status, COALESCE(failure_id, ''), source_attempt_id,
			       COALESCE(target_attempt_id, '')
			FROM attempt_handoffs WHERE id = ?`, input.HandoffID,
		).Scan(
			&handoffStatus, &handoffFailureID, &handoffSourceAttemptID, &handoffTargetAttemptID,
		); err == sql.ErrNoRows {
			return false, nil
		} else if err != nil {
			return false, fmt.Errorf("reinstate accepted candidate: inspect handoff: %w", err)
		} else if handoffFailureID != input.FailureID || handoffSourceAttemptID != input.SourceAttemptID {
			return false, nil
		}
		if handoffTargetAttemptID != "" {
			err := tx.QueryRowContext(ctx, `
				SELECT target.status, target.session_id, target.resource_id, target.retry_number,
				       (SELECT COUNT(*) FROM execution_manifests AS execution
				        WHERE execution.attempt_id = target.id),
				       (SELECT COUNT(*) FROM candidate_sets AS candidate
				        WHERE candidate.attempt_id = target.id),
				       (SELECT COUNT(*) FROM dispatch_lifecycles AS lifecycle
				        WHERE lifecycle.attempt_id = target.id),
				       COALESCE((SELECT lifecycle.id FROM dispatch_lifecycles AS lifecycle
				                 WHERE lifecycle.attempt_id = target.id LIMIT 1), ''),
				       (SELECT COUNT(*) FROM dispatch_lifecycles AS lifecycle
				        WHERE lifecycle.attempt_id = target.id
				          AND (lifecycle.host_session_id != '' OR lifecycle.worker_id != ''
				               OR lifecycle.dispatch_requested_at != ''
				               OR lifecycle.worker_acknowledged_at != ''
				               OR lifecycle.execution_registered_at != ''
				               OR lifecycle.candidate_submitted_at != ''
				               OR lifecycle.candidate_accepted_at != ''
				               OR lifecycle.candidate_rejected_at != ''))
				FROM generation_attempts AS target
				WHERE target.id = ?`, handoffTargetAttemptID,
			).Scan(
				&handoffTargetStatus, &handoffTargetSessionID, &handoffTargetResourceID,
				&handoffTargetRetryNumber, &targetExecutions, &targetCandidates,
				&targetLifecycleCount, &targetLifecycleID, &targetDispatchActivity,
			)
			if err == sql.ErrNoRows {
				return false, nil
			}
			if err != nil {
				return false, fmt.Errorf("reinstate accepted candidate: inspect handoff target: %w", err)
			}
		}
	}

	resolverStatus, resolverSessionID, resolverResourceID := "", "", ""
	resolverAcceptedExecutions, resolverAcceptedCandidates := 0, 0
	if resolvedByAttempt != "" {
		err := tx.QueryRowContext(ctx, `
			SELECT resolver.status, resolver.session_id, resolver.resource_id,
			       (SELECT COUNT(*) FROM execution_manifests AS execution
			        WHERE execution.attempt_id = resolver.id AND execution.status = 'accepted'),
			       (SELECT COUNT(*) FROM candidate_sets AS candidate
			        WHERE candidate.attempt_id = resolver.id AND candidate.status = 'accepted')
			FROM generation_attempts AS resolver
			WHERE resolver.id = ?`, resolvedByAttempt,
		).Scan(
			&resolverStatus, &resolverSessionID, &resolverResourceID,
			&resolverAcceptedExecutions, &resolverAcceptedCandidates,
		)
		if err != nil && err != sql.ErrNoRows {
			return false, fmt.Errorf("reinstate accepted candidate: inspect resolver attempt: %w", err)
		}
	}

	recoverableProjection := resourceState == "pending" && jobID == "" && executionStrategy == "generate" &&
		((phase == "integration_failed" && lastError == "") || phase == "pre_execution_recovered")
	safePreparedHandoffTarget := input.HandoffID != "" && handoffStatus == "accepted" &&
		handoffTargetStatus == "context_prepared" && handoffTargetSessionID == input.SessionID &&
		handoffTargetResourceID == input.ResourceID && handoffTargetRetryNumber > sourceRetryNumber &&
		targetExecutions == 0 && targetCandidates == 0 && targetLifecycleCount == 1 &&
		targetLifecycleID != "" && targetDispatchActivity == 0
	safeAbandonedHandoffTarget := input.HandoffID != "" && handoffStatus == "accepted" &&
		handoffTargetStatus == "abandoned" && handoffTargetSessionID == input.SessionID &&
		handoffTargetResourceID == input.ResourceID && handoffTargetRetryNumber > sourceRetryNumber &&
		targetExecutions == 0 && targetCandidates == 0 && targetLifecycleCount == 1 &&
		targetLifecycleID != "" && targetDispatchActivity == 0
	handofflessFailureEligible := failureHandoffs == 0 &&
		((failureCategory == "invalid_validation" && blocksRetry == 1) ||
			(failureCategory == "integration_error" && exhaustionFailureID == input.FailureID &&
				otherUnresolvedClosureFailures == 0 && laterClosureFailures == 0))
	handoffEligible := (input.HandoffID != "" && failureHandoffs == 1 &&
		otherUnresolvedClosureFailures == 0 && laterClosureFailures == 0 &&
		((handoffStatus == "pending" && handoffTargetAttemptID == "") ||
			safePreparedHandoffTarget || safeAbandonedHandoffTarget)) ||
		(input.HandoffID == "" && handofflessFailureEligible)
	crossOwnerResolverEligible := resolvedByAttempt != "" && input.HandoffID != "" &&
		input.ResolverAttemptID == resolvedByAttempt && resolverStatus == "accepted" &&
		resolverSessionID == input.SessionID && resolverResourceID != "" &&
		resolverResourceID != input.ResourceID && resolverAcceptedExecutions == 1 &&
		resolverAcceptedCandidates == 1
	resolutionEligible := (resolvedByAttempt == "" && input.ResolverAttemptID == "") ||
		crossOwnerResolverEligible
	newerHistoryEligible := unsafeNewerAttempts == 0 ||
		(safePreparedHandoffTarget && unsafeNewerAttempts == 1)
	eligible := candidateHash == input.CandidateHash && generationInputHash == input.GenerationInputHash &&
		dependencyContractHash == input.DependencyContractHash && planOperationID == input.PlanOperationID &&
		attemptStatus == "accepted" && recoverableProjection && handoffEligible &&
		candidateStatus == "accepted" && failureCategory == input.FailureCategory &&
		failureOrigin == "engine" && failureSource == "affected_closure_validation" &&
		failureResolution == input.Reason && resolvedAt != "" && resolutionEligible &&
		newerHistoryEligible
	if !eligible {
		return false, fmt.Errorf("reinstate accepted candidate: durable eligibility changed: candidate_hash=%t generation_hash=%t dependency_hash=%t operation=%t attempt=%q state=%q phase=%q last_error_empty=%t job_empty=%t strategy=%q handoff=%q candidate=%q failure_category=%q failure_origin=%q failure_source=%q resolved=%t resolution_eligible=%t cross_owner_resolver=%t safe_prepared_target=%t safe_abandoned_target=%t exhaustion_link=%t failure_handoffs=%d unresolved_competitors=%d later_closure_failures=%d unsafe_newer=%d",
			candidateHash == input.CandidateHash, generationInputHash == input.GenerationInputHash,
			dependencyContractHash == input.DependencyContractHash, planOperationID == input.PlanOperationID,
			attemptStatus, resourceState, phase, lastError == "", jobID == "", executionStrategy,
			handoffStatus, candidateStatus, failureCategory, failureOrigin, failureSource, resolvedAt != "",
			resolutionEligible, crossOwnerResolverEligible, safePreparedHandoffTarget,
			safeAbandonedHandoffTarget, exhaustionFailureID == input.FailureID, failureHandoffs,
			otherUnresolvedClosureFailures, laterClosureFailures, unsafeNewerAttempts)
	}

	// A shared module file may legitimately differ from this candidate when a
	// later accepted candidate in the same active session composed additional
	// declarations into it. Recheck every such exception inside this transaction
	// before changing the resource projection. No repository bytes are written.
	supersededPaths := make([]string, 0, len(input.SupersededFiles))
	for path := range input.SupersededFiles {
		supersededPaths = append(supersededPaths, path)
	}
	sort.Strings(supersededPaths)
	for _, path := range supersededPaths {
		var proven int
		err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM candidate_sets AS source_candidate
			JOIN generation_attempts AS source_attempt ON source_attempt.id = source_candidate.attempt_id
			JOIN candidate_files AS source_file ON source_file.candidate_id = source_candidate.id
			JOIN generation_attempts AS later_attempt
			  ON later_attempt.session_id = source_attempt.session_id
			JOIN candidate_sets AS later_candidate
			  ON later_candidate.attempt_id = later_attempt.id
			JOIN candidate_files AS later_file ON later_file.candidate_id = later_candidate.id
			WHERE source_candidate.id = ? AND source_attempt.id = ?
			  AND source_file.path = ?
			  AND later_attempt.id != source_attempt.id
			  AND later_attempt.status = 'accepted' AND later_candidate.status = 'accepted'
			  AND later_candidate.disposed_at IS NOT NULL
			  AND source_candidate.disposed_at IS NOT NULL
			  AND later_candidate.disposed_at > source_candidate.disposed_at
			  AND later_file.path = ? AND later_file.content_hash = ?`,
			input.CandidateID, input.SourceAttemptID, path, path, input.SupersededFiles[path],
		).Scan(&proven)
		if err != nil {
			return false, fmt.Errorf("reinstate accepted candidate: verify superseded file %s: %w", path, err)
		}
		if proven == 0 {
			return false, fmt.Errorf("reinstate accepted candidate: superseded file %s has no later accepted provenance", path)
		}
	}

	timestamp := now()
	if safePreparedHandoffTarget {
		abandoned, abandonErr := tx.ExecContext(ctx, `
			UPDATE generation_attempts
			SET status = 'abandoned'
			WHERE id = ? AND status = 'context_prepared'
			  AND NOT EXISTS (SELECT 1 FROM execution_manifests AS execution
			                  WHERE execution.attempt_id = generation_attempts.id)
			  AND NOT EXISTS (SELECT 1 FROM candidate_sets AS candidate
			                  WHERE candidate.attempt_id = generation_attempts.id)
			  AND EXISTS (
			      SELECT 1 FROM dispatch_lifecycles AS lifecycle
			      WHERE lifecycle.id = ? AND lifecycle.attempt_id = generation_attempts.id
			        AND lifecycle.host_session_id = ''
			        AND lifecycle.worker_id = ''
			        AND lifecycle.dispatch_requested_at = ''
			        AND lifecycle.worker_acknowledged_at = ''
			        AND lifecycle.execution_registered_at = ''
			        AND lifecycle.candidate_submitted_at = ''
			        AND lifecycle.candidate_accepted_at = ''
			        AND lifecycle.candidate_rejected_at = ''
			  )`, handoffTargetAttemptID, targetLifecycleID)
		if abandonErr != nil {
			return false, fmt.Errorf("reinstate accepted candidate: abandon unclaimed handoff context: %w", abandonErr)
		}
		abandonedCount, rowsErr := abandoned.RowsAffected()
		if rowsErr != nil || abandonedCount != 1 {
			return false, fmt.Errorf(
				"reinstate accepted candidate: abandon unclaimed handoff context affected=%d: %w",
				abandonedCount, rowsErr,
			)
		}
		if _, auditErr := createFailureClassificationTx(
			ctx, s.queries.WithTx(tx), FailureClassificationWrite{
				AttemptID: handoffTargetAttemptID, Category: execution.FailureStaleContext,
				Origin: "engine", Confidence: 1,
				EvidenceSource:    "resolved_candidate_reinstatement",
				EvidenceReference: input.HandoffID,
				Evidence:          "retired an unclaimed prepared repair context after its exact source failure was resolved by an accepted different-resource attempt",
			}, timestamp,
		); auditErr != nil {
			return false, fmt.Errorf("reinstate accepted candidate: audit retired handoff context: %w", auditErr)
		}
	}
	if input.HandoffID != "" {
		handoffResult, err := tx.ExecContext(ctx, `
			UPDATE attempt_handoffs SET status = 'cancelled'
			WHERE id = ? AND status = ? AND source_attempt_id = ?
			  AND failure_id = ? AND COALESCE(target_attempt_id, '') = ?`,
			input.HandoffID, handoffStatus, input.SourceAttemptID,
			input.FailureID, handoffTargetAttemptID,
		)
		if err != nil {
			return false, fmt.Errorf("reinstate accepted candidate: cancel handoff: %w", err)
		}
		handoffChanged, err := handoffResult.RowsAffected()
		if err != nil || handoffChanged != 1 {
			return false, fmt.Errorf("reinstate accepted candidate: cancel handoff affected=%d: %w", handoffChanged, err)
		}
	}
	resourceResult, err := tx.ExecContext(ctx, `
		UPDATE session_resources
		SET state = 'committed', phase = 'integration_pending', last_error = NULL,
		    last_output = NULL, job_id = NULL, exhaustion_failure_id = NULL, updated_at = ?
		WHERE session_id = ? AND resource_id = ? AND state = 'pending'
		  AND execution_strategy = 'generate' AND COALESCE(job_id, '') = ''
		  AND ((phase = 'integration_failed' AND COALESCE(last_error, '') = '')
		       OR phase = 'pre_execution_recovered')
		  AND (? != '' OR ? != 'integration_error' OR exhaustion_failure_id = ?)`,
		timestamp, input.SessionID, input.ResourceID,
		input.HandoffID, failureCategory, input.FailureID)
	if err != nil {
		return false, fmt.Errorf("reinstate accepted candidate: update resource: %w", err)
	}
	resourceChanged, err := resourceResult.RowsAffected()
	if err != nil || resourceChanged != 1 {
		return false, fmt.Errorf("reinstate accepted candidate: update resource affected=%d: %w", resourceChanged, err)
	}
	actionID := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO apply_actions (id, apply_id, resource_id, action, outcome, error, started_at, done_at)
		VALUES (?, ?, ?, 'reinstate_candidate', 'reinstated', NULL, ?, ?)`,
		actionID, applyID, input.ResourceID, timestamp, timestamp,
	); err != nil {
		return false, fmt.Errorf("reinstate accepted candidate: record audit action: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("reinstate accepted candidate: commit: %w", err)
	}
	return true, nil
}

// RestoreValidateExistingSessionResource repairs the other projection damaged
// by the legacy invalid-validation reopen behavior. The immutable session plan
// remains authoritative: only an original validate_existing action with exact
// current operation fingerprints, zero generation history, and the precise
// cleared integration-failure projection can be restored. No candidate or
// model attempt is invented.
func (s *Store) RestoreValidateExistingSessionResource(
	ctx context.Context,
	input ValidateExistingReinstatement,
) (bool, error) {
	if input.SessionID == "" || input.ResourceID == "" || input.PlanOperationID == "" ||
		input.GenerationInputHash == "" || input.DependencyContractHash == "" ||
		strings.TrimSpace(input.Reason) == "" {
		return false, fmt.Errorf("restore validate-existing resource: complete operation identity and reason are required")
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("restore validate-existing resource: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		status, applyID, planJSON, state, phase, lastError, jobID string
		executionStrategy, sourceCandidateID                      string
		attempts, generationAttempts                              int
	)
	err = tx.QueryRowContext(ctx, `
		SELECT session.status, session.apply_id, session.plan_json,
		       resource.state, resource.phase, COALESCE(resource.last_error, ''),
		       COALESCE(resource.job_id, ''), resource.execution_strategy,
		       COALESCE(resource.source_candidate_id, ''), resource.attempts,
		       (SELECT COUNT(*) FROM generation_attempts AS attempt
		        WHERE attempt.session_id = resource.session_id AND attempt.resource_id = resource.resource_id)
		FROM agent_sessions AS session
		JOIN session_resources AS resource ON resource.session_id = session.id
		WHERE session.id = ? AND resource.resource_id = ?`, input.SessionID, input.ResourceID,
	).Scan(
		&status, &applyID, &planJSON, &state, &phase, &lastError, &jobID,
		&executionStrategy, &sourceCandidateID, &attempts, &generationAttempts,
	)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("restore validate-existing resource: inspect projection: %w", err)
	}

	type immutableAction struct {
		ResourceID             string
		OperationID            string `json:"operation_id"`
		GenerationInputHash    string `json:"generation_input_hash"`
		DependencyContractHash string `json:"dependency_contract_hash"`
		ExecutionStrategy      string `json:"execution_strategy"`
		SourceCandidateID      string `json:"source_candidate_id"`
	}
	var actions []immutableAction
	if err := json.Unmarshal([]byte(planJSON), &actions); err != nil {
		return false, fmt.Errorf("restore validate-existing resource: decode immutable plan: %w", err)
	}
	var action *immutableAction
	for index := range actions {
		if actions[index].ResourceID == input.ResourceID {
			action = &actions[index]
			break
		}
	}
	planMatches := action != nil && action.ExecutionStrategy == "validate_existing" &&
		action.SourceCandidateID == "" && action.OperationID == input.PlanOperationID &&
		action.GenerationInputHash == input.GenerationInputHash &&
		action.DependencyContractHash == input.DependencyContractHash
	eligible := status == "active" && planMatches && state == "pending" &&
		phase == "integration_failed" && lastError == "" && jobID == "" &&
		executionStrategy == "generate" && sourceCandidateID == "" && attempts == 0 && generationAttempts == 0
	if !eligible {
		return false, nil
	}

	timestamp := now()
	result, err := tx.ExecContext(ctx, `
		UPDATE session_resources
		SET state = 'committed', phase = 'integration_pending', execution_strategy = 'validate_existing',
		    source_candidate_id = NULL, exhaustion_failure_id = NULL,
		    last_error = NULL, last_output = NULL, job_id = NULL, updated_at = ?
		WHERE session_id = ? AND resource_id = ? AND state = 'pending'
		  AND phase = 'integration_failed' AND COALESCE(last_error, '') = ''
		  AND COALESCE(job_id, '') = '' AND execution_strategy = 'generate'
		  AND source_candidate_id IS NULL AND attempts = 0`, timestamp, input.SessionID, input.ResourceID)
	if err != nil {
		return false, fmt.Errorf("restore validate-existing resource: update projection: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return false, fmt.Errorf("restore validate-existing resource: update affected=%d: %w", changed, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO apply_actions (id, apply_id, resource_id, action, outcome, error, started_at, done_at)
		VALUES (?, ?, ?, 'restore_validate_existing', 'restored', NULL, ?, ?)`,
		uuid.NewString(), applyID, input.ResourceID, timestamp, timestamp,
	); err != nil {
		return false, fmt.Errorf("restore validate-existing resource: record audit action: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("restore validate-existing resource: commit: %w", err)
	}
	return true, nil
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
