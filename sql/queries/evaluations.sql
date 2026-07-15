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
