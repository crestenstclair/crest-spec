-- name: DeactivateVerificationDefinitions :exec
UPDATE verification_definitions SET active = 0, updated_at = ? WHERE project_name = ?;

-- name: UpsertVerificationDefinition :exec
INSERT INTO verification_definitions (
    snapshot_id, definition_id, project_name, definition_type, scope, kind,
    definition_hash, definition_json, spec_hash, active, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
ON CONFLICT(snapshot_id) DO UPDATE SET
    active = 1,
    spec_hash = excluded.spec_hash,
    updated_at = excluded.updated_at;

-- name: ClearVerificationDefinitionTargets :exec
DELETE FROM verification_definition_targets WHERE snapshot_id = ?;

-- name: InsertVerificationDefinitionTarget :exec
INSERT INTO verification_definition_targets (snapshot_id, target_kind, target_id) VALUES (?, ?, ?);

-- name: GetActiveVerificationDefinition :one
SELECT * FROM verification_definitions
WHERE project_name = ? AND definition_id = ? AND active = 1;

-- name: GetVerificationDefinition :one
SELECT * FROM verification_definitions WHERE snapshot_id = ?;

-- name: ListActiveVerificationDefinitions :many
SELECT * FROM verification_definitions
WHERE project_name = ? AND active = 1
ORDER BY definition_type, scope, definition_id;

-- name: ListVerificationDefinitionTargets :many
SELECT * FROM verification_definition_targets
WHERE snapshot_id = ? ORDER BY target_kind, target_id;

-- name: PutValidationRun :exec
INSERT INTO validation_runs (
    id, definition_snapshot_id, definition_id, session_id, attempt_id,
    execution_id, source_tree_hash, classification, reason, started_at,
    completed_at, duration_ms, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: PutValidationRunTarget :exec
INSERT INTO validation_run_targets (run_id, target_kind, target_id) VALUES (?, ?, ?);

-- name: PutValidationExecution :exec
INSERT INTO validation_executions (
    id, run_id, execution_role, command_json, command_hash,
    working_directory, environment_json, environment_hash, source_tree_hash,
    executable_hash, stdout_blob, stderr_blob, stdout_hash, stderr_hash,
    stdout_bytes, stderr_bytes, stdout_truncated, stderr_truncated,
    exit_code, timed_out, launch_error, started_at, completed_at, duration_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: PutValidationArtifact :exec
INSERT INTO validation_artifacts (
    execution_id, path, content_blob, content_hash, byte_size,
    captured_bytes, truncated, missing
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: PutParsedObservation :exec
INSERT INTO parsed_observations (
    execution_id, schema_json, schema_hash, observation_json,
    observation_hash, parse_error
) VALUES (?, ?, ?, ?, ?, ?);

-- name: PutValidationPredicateResult :exec
INSERT INTO validation_predicate_results (
    run_id, execution_id, ordinal, field_name, operator, expected_json,
    observed_json, passed, reason
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: PutVerificationEvidenceRecord :exec
INSERT INTO verification_evidence_records (
    id, run_id, evidence_id, source_tree_hash, definition_hash,
    classification, currency, created_at
) VALUES (?, ?, ?, ?, ?, ?, 'current', ?);

-- name: GetValidationRun :one
SELECT * FROM validation_runs WHERE id = ?;

-- name: ListValidationRuns :many
SELECT * FROM validation_runs ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: ListValidationRunsByDefinition :many
SELECT * FROM validation_runs WHERE definition_id = ? ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: ListValidationExecutionsByRun :many
SELECT * FROM validation_executions WHERE run_id = ? ORDER BY execution_role;

-- name: ListValidationRunTargets :many
SELECT * FROM validation_run_targets WHERE run_id = ? ORDER BY target_kind, target_id;

-- name: ListValidationArtifactsByExecution :many
SELECT * FROM validation_artifacts WHERE execution_id = ? ORDER BY path;

-- name: GetParsedObservation :one
SELECT * FROM parsed_observations WHERE execution_id = ?;

-- name: ListValidationPredicateResults :many
SELECT * FROM validation_predicate_results WHERE run_id = ? ORDER BY execution_id, ordinal;

-- name: ListVerificationEvidenceByRun :many
SELECT * FROM verification_evidence_records WHERE run_id = ? ORDER BY evidence_id;

-- name: ListCurrentVerificationEvidence :many
SELECT * FROM verification_evidence_records
WHERE evidence_id = ? AND currency = 'current'
ORDER BY created_at DESC, id DESC;

-- name: ListLatestValidationRunsForProject :many
SELECT vr.definition_id, vr.id AS run_id, vr.classification, vr.source_tree_hash, vr.created_at
FROM validation_runs vr
JOIN verification_definitions vd ON vd.snapshot_id = vr.definition_snapshot_id
WHERE vd.project_name = ? AND vd.active = 1
  AND vr.id = (
      SELECT latest.id FROM validation_runs latest
      WHERE latest.definition_id = vr.definition_id
      ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1
  )
ORDER BY vr.definition_id;

-- name: ListCurrentVerificationEvidenceForProject :many
SELECT record.*
FROM verification_evidence_records record
JOIN evidence_requirements requirement ON requirement.id = record.evidence_id
WHERE requirement.project_name = ? AND requirement.active = 1 AND record.currency = 'current'
ORDER BY record.evidence_id, record.created_at DESC, record.id DESC;

-- name: MarkVerificationEvidenceStale :many
UPDATE verification_evidence_records
SET currency = 'stale', invalidated_at = ?
WHERE evidence_id = ? AND currency = 'current'
RETURNING *;

-- name: ListCurrentVerificationEvidenceForResource :many
SELECT DISTINCT record.*
FROM verification_evidence_records record
JOIN validation_runs run ON run.id = record.run_id
JOIN verification_definitions definition ON definition.snapshot_id = run.definition_snapshot_id
JOIN verification_definition_targets target ON target.snapshot_id = definition.snapshot_id
WHERE definition.project_name = ? AND target.target_kind = 'resource' AND target.target_id = ?
  AND record.currency = 'current'
ORDER BY record.created_at, record.id;

-- name: MarkVerificationEvidenceRecordStale :execrows
UPDATE verification_evidence_records
SET currency = 'stale', invalidated_at = ?
WHERE id = ? AND currency = 'current';

-- name: PutVerificationEvidenceInvalidation :exec
INSERT INTO verification_evidence_invalidations (
    id, evidence_record_id, reason, source_type, source_id, created_at
) VALUES (?, ?, ?, ?, ?, ?);
