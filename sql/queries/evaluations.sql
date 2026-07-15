-- name: PutEvaluationCaseAssessment :exec
INSERT INTO evaluation_case_assessments (
    source_attempt_id, eligible, reason, case_id, assessed_at
) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(source_attempt_id) DO UPDATE SET
    eligible = excluded.eligible,
    reason = excluded.reason,
    case_id = excluded.case_id,
    assessed_at = excluded.assessed_at;

-- name: GetEvaluationCaseAssessment :one
SELECT * FROM evaluation_case_assessments WHERE source_attempt_id = ?;

-- name: PutEvaluationCase :exec
INSERT INTO evaluation_cases (
    id, identity_hash, provenance, source_attempt_id, project_name, goal_id,
    capability_id, resource_id, spec_hash, repository_hash,
    resource_declaration_hash, plan_operation_id, context_manifest_id,
    execution_id, candidate_id, payload_blob, expected_outcome_blob, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(identity_hash) DO NOTHING;

-- name: GetEvaluationCase :one
SELECT * FROM evaluation_cases WHERE id = ?;

-- name: GetEvaluationCaseByIdentity :one
SELECT * FROM evaluation_cases WHERE identity_hash = ?;

-- name: ListEvaluationCases :many
SELECT * FROM evaluation_cases ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: ListEvaluationSourceValidationRuns :many
SELECT * FROM validation_runs
WHERE attempt_id = ?
ORDER BY created_at DESC, id DESC;

-- name: PutEvaluationDataset :exec
INSERT INTO evaluation_datasets (
    id, name, description, identity_hash, status, created_at, sealed_at
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetEvaluationDataset :one
SELECT * FROM evaluation_datasets WHERE id = ?;

-- name: ListEvaluationDatasets :many
SELECT * FROM evaluation_datasets ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: AddEvaluationDatasetCase :exec
INSERT INTO evaluation_dataset_cases (dataset_id, case_id, split, ordinal)
VALUES (?, ?, ?, ?);

-- name: ListEvaluationDatasetCases :many
SELECT membership.*, evaluation_cases.identity_hash, evaluation_cases.provenance,
       evaluation_cases.project_name, evaluation_cases.goal_id,
       evaluation_cases.capability_id, evaluation_cases.resource_id,
       evaluation_cases.context_manifest_id, evaluation_cases.execution_id,
       evaluation_cases.candidate_id
FROM evaluation_dataset_cases membership
JOIN evaluation_cases ON evaluation_cases.id = membership.case_id
WHERE membership.dataset_id = ?
ORDER BY membership.ordinal;

-- name: SealEvaluationDataset :execrows
UPDATE evaluation_datasets
SET identity_hash = ?, status = 'sealed', sealed_at = ?
WHERE id = ? AND status = 'draft';

-- name: PutEvaluationConfiguration :exec
INSERT INTO evaluation_configurations (
    id, name, identity_hash, planner_version, planner_policy_hash,
    context_selector_version, context_budget_tokens, template_hashes_json,
    role_policy_version, host_name, host_version, provider, model,
    inference_config_json, validation_version, learning_ids_json,
    payload_blob, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(identity_hash) DO NOTHING;

-- name: GetEvaluationConfiguration :one
SELECT * FROM evaluation_configurations WHERE id = ?;

-- name: GetEvaluationConfigurationByIdentity :one
SELECT * FROM evaluation_configurations WHERE identity_hash = ?;

-- name: ListEvaluationConfigurations :many
SELECT * FROM evaluation_configurations ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: PutEvaluationRun :exec
INSERT INTO evaluation_runs (
    id, dataset_id, name, status, metric_policy_json, metric_policy_hash,
    minimum_sample_size, practical_significance, require_held_out,
    conclusion, winning_variant, conclusion_reason, created_at, started_at,
    completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: PutEvaluationRunVariant :exec
INSERT INTO evaluation_run_variants (
    run_id, variant_name, configuration_id, is_baseline, ordinal
) VALUES (?, ?, ?, ?, ?);

-- name: PutEvaluationAssignment :exec
INSERT INTO evaluation_assignments (
    id, run_id, case_id, variant_name, configuration_id, split, status,
    current_lease_id, lease_owner, lease_expires_at, attempt_id,
    terminal_status, terminal_reason, submitted_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetEvaluationRun :one
SELECT * FROM evaluation_runs WHERE id = ?;

-- name: ListEvaluationRuns :many
SELECT * FROM evaluation_runs ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: ListEvaluationRunVariants :many
SELECT variants.*, configurations.name AS configuration_name,
       configurations.identity_hash AS configuration_identity_hash
FROM evaluation_run_variants variants
JOIN evaluation_configurations configurations ON configurations.id = variants.configuration_id
WHERE variants.run_id = ?
ORDER BY variants.ordinal;

-- name: ListEvaluationAssignments :many
SELECT * FROM evaluation_assignments
WHERE run_id = ?
ORDER BY split, case_id, variant_name;

-- name: GetEvaluationAssignment :one
SELECT * FROM evaluation_assignments WHERE id = ?;

-- name: RequeueExpiredEvaluationAssignments :execrows
UPDATE evaluation_assignments
SET status = 'pending', current_lease_id = NULL, lease_owner = '',
    lease_expires_at = NULL, updated_at = ?
WHERE run_id = ? AND status = 'leased' AND lease_expires_at <= ?;

-- name: ExpireEvaluationAssignmentLeases :execrows
UPDATE evaluation_assignment_leases
SET status = 'expired', completed_at = ?
WHERE status = 'active' AND expires_at <= ?
  AND assignment_id IN (SELECT id FROM evaluation_assignments WHERE run_id = ?);

-- name: GetNextEvaluationAssignment :one
SELECT * FROM evaluation_assignments
WHERE run_id = sqlc.arg(run_id) AND status = 'pending'
  AND (sqlc.arg(split_filter) = '' OR split = sqlc.arg(split_filter))
ORDER BY CASE split WHEN 'training' THEN 0 WHEN 'development' THEN 1 ELSE 2 END,
         case_id, variant_name
LIMIT 1;

-- name: ClaimEvaluationAssignment :execrows
UPDATE evaluation_assignments
SET status = 'leased', current_lease_id = ?, lease_owner = ?,
    lease_expires_at = ?, updated_at = ?
WHERE id = ? AND status = 'pending';

-- name: CountEvaluationAssignmentLeases :one
SELECT COUNT(*) FROM evaluation_assignment_leases WHERE assignment_id = ?;

-- name: PutEvaluationAssignmentLease :exec
INSERT INTO evaluation_assignment_leases (
    id, assignment_id, lease_owner, lease_token_hash, lease_number,
    status, claimed_at, expires_at, completed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetEvaluationAssignmentLease :one
SELECT * FROM evaluation_assignment_leases WHERE id = ?;

-- name: HeartbeatEvaluationAssignmentLease :execrows
UPDATE evaluation_assignment_leases
SET expires_at = ?
WHERE id = ? AND assignment_id = ? AND lease_owner = ?
  AND lease_token_hash = ? AND status = 'active' AND expires_at > ?;

-- name: UpdateEvaluationAssignmentLeaseExpiry :execrows
UPDATE evaluation_assignments
SET lease_expires_at = ?, updated_at = ?
WHERE id = ? AND current_lease_id = ? AND status = 'leased';

-- name: CompleteEvaluationAssignmentLease :execrows
UPDATE evaluation_assignment_leases
SET status = ?, completed_at = ?
WHERE id = ? AND assignment_id = ? AND lease_owner = ?
  AND lease_token_hash = ? AND status = 'active';

-- name: ReleaseEvaluationAssignment :execrows
UPDATE evaluation_assignments
SET status = 'pending', current_lease_id = NULL, lease_owner = '',
    lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND current_lease_id = ? AND status = 'leased';

-- name: SubmitEvaluationAssignment :execrows
UPDATE evaluation_assignments
SET status = 'submitted', attempt_id = ?, terminal_status = ?,
    terminal_reason = ?, submitted_at = ?, current_lease_id = NULL,
    lease_owner = '', lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND current_lease_id = ? AND status = 'leased';

-- name: CancelEvaluationAssignment :execrows
UPDATE evaluation_assignments
SET status = 'cancelled', terminal_status = 'cancelled', terminal_reason = ?,
    submitted_at = ?, current_lease_id = NULL, lease_owner = '',
    lease_expires_at = NULL, updated_at = ?
WHERE id = ? AND status IN ('pending','leased');

-- name: PutEvaluationMetricObservation :exec
INSERT INTO evaluation_metric_observations (
    assignment_id, metric_name, value, missing_reason, unit,
    source_type, source_id, metadata_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListEvaluationMetricObservationsByRun :many
SELECT observation.*, assignment.run_id, assignment.case_id,
       assignment.variant_name, assignment.split
FROM evaluation_metric_observations observation
JOIN evaluation_assignments assignment ON assignment.id = observation.assignment_id
WHERE assignment.run_id = ?
ORDER BY assignment.variant_name, assignment.split, assignment.case_id, observation.metric_name;

-- name: ClearEvaluationRunAnalysis :exec
DELETE FROM evaluation_metric_aggregates WHERE run_id = ?;

-- name: ClearEvaluationRunComparisons :exec
DELETE FROM evaluation_comparisons WHERE run_id = ?;

-- name: PutEvaluationMetricAggregate :exec
INSERT INTO evaluation_metric_aggregates (
    run_id, variant_name, split, metric_name, sample_count, missing_count,
    mean_value, minimum_value, maximum_value, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: PutEvaluationComparison :exec
INSERT INTO evaluation_comparisons (
    run_id, baseline_variant, candidate_variant, split, metric_name,
    baseline_sample_count, candidate_sample_count, missing_count,
    baseline_value, candidate_value, absolute_change, relative_change,
    practical_threshold, conclusion, regression, reason, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListEvaluationMetricAggregates :many
SELECT * FROM evaluation_metric_aggregates
WHERE run_id = ? ORDER BY split, variant_name, metric_name;

-- name: ListEvaluationComparisons :many
SELECT * FROM evaluation_comparisons
WHERE run_id = ? ORDER BY split, candidate_variant, metric_name;

-- name: CompleteEvaluationRun :execrows
UPDATE evaluation_runs
SET status = 'completed', conclusion = ?, winning_variant = ?,
    conclusion_reason = ?, completed_at = ?
WHERE id = ? AND status = 'running';

-- name: PutEvaluationPromotionProposal :exec
INSERT INTO evaluation_promotion_proposals (
    id, run_id, configuration_id, variant_name, change_kind,
    target_identity, change_blob, rollback_identity, status,
    eligibility_reason, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (run_id, configuration_id, variant_name, change_kind, target_identity, change_blob)
DO NOTHING;

-- name: GetEvaluationPromotionProposal :one
SELECT proposal.*, configuration.name AS configuration_name,
       configuration.identity_hash AS configuration_identity_hash,
       blob.content AS change_content
FROM evaluation_promotion_proposals proposal
JOIN evaluation_configurations configuration ON configuration.id = proposal.configuration_id
JOIN content_blobs blob ON blob.hash = proposal.change_blob
WHERE proposal.id = ?;

-- name: GetEvaluationPromotionProposalByIdentity :one
SELECT proposal.*, configuration.name AS configuration_name,
       configuration.identity_hash AS configuration_identity_hash,
       blob.content AS change_content
FROM evaluation_promotion_proposals proposal
JOIN evaluation_configurations configuration ON configuration.id = proposal.configuration_id
JOIN content_blobs blob ON blob.hash = proposal.change_blob
WHERE proposal.run_id = ? AND proposal.configuration_id = ?
  AND proposal.variant_name = ? AND proposal.change_kind = ?
  AND proposal.target_identity = ? AND proposal.change_blob = ?;

-- name: ListEvaluationPromotionProposals :many
SELECT proposal.*, configuration.name AS configuration_name,
       configuration.identity_hash AS configuration_identity_hash,
       blob.content AS change_content
FROM evaluation_promotion_proposals proposal
JOIN evaluation_configurations configuration ON configuration.id = proposal.configuration_id
JOIN content_blobs blob ON blob.hash = proposal.change_blob
WHERE (sqlc.arg(status_filter) = '' OR proposal.status = sqlc.arg(status_filter))
ORDER BY proposal.created_at DESC, proposal.id DESC
LIMIT sqlc.arg(row_limit);

-- name: ListEvaluationPromotionDecisions :many
SELECT * FROM evaluation_promotion_decisions
WHERE proposal_id = ? ORDER BY created_at, id;

-- name: UpdateEvaluationPromotionStatus :execrows
UPDATE evaluation_promotion_proposals
SET status = ?, updated_at = ?
WHERE id = ? AND status = ?;

-- name: PutEvaluationPromotionDecision :exec
INSERT INTO evaluation_promotion_decisions (
    id, proposal_id, decision, actor, reason, created_at
) VALUES (?, ?, ?, ?, ?, ?);
