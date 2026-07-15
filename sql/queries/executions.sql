-- name: CreateExecutionManifest :exec
INSERT INTO execution_manifests (
    id, attempt_id, context_manifest_id, protocol_version, idempotency_key,
    plan_operation_id, role, role_policy_version, context_policy,
    host_name, host_version, provider, model,
    inference_config_json, inference_config_hash,
    agent_config_json, agent_config_hash,
    tool_permissions_json, tool_permissions_hash,
    template_hashes_json, template_hashes_hash,
    system_instructions_blob, context_hash, host_session_id,
    status, disposition_reason, started_at, completed_at,
    duration_ms, input_tokens, output_tokens, cost_usd, created_at, updated_at,
    host_commit_ref, goal_progress_json, goal_progress_hash
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateGenerationAttemptStatus :execrows
UPDATE generation_attempts SET status = ? WHERE id = ? AND status = ?;

-- name: CreateExecutionGeneration :exec
INSERT INTO generations (
    id, apply_id, resource_id, prompt_text, prompt_hash, output_text, model,
    outcome, rejection_reason, retry_count, duration_ms, input_tokens,
    output_tokens, cost_usd, created_at, execution_id
) VALUES (?, ?, ?, '', ?, '', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetExecutionManifest :one
SELECT * FROM execution_manifests WHERE id = ?;

-- name: GetExecutionManifestByAttempt :one
SELECT * FROM execution_manifests WHERE attempt_id = ?;

-- name: GetExecutionManifestByIdempotencyKey :one
SELECT * FROM execution_manifests WHERE idempotency_key = ?;

-- name: ListRecentExecutionManifests :many
SELECT * FROM execution_manifests ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: UpdateExecutionManifestStatus :execrows
UPDATE execution_manifests
SET status = ?, disposition_reason = ?, completed_at = ?, duration_ms = ?,
    input_tokens = ?, output_tokens = ?, cost_usd = ?, updated_at = ?,
    host_commit_ref = ?, goal_progress_json = ?, goal_progress_hash = ?
WHERE id = ? AND status = ?;

-- name: AddExecutionGoal :exec
INSERT INTO execution_goals (execution_id, goal_id) VALUES (?, ?);

-- name: ListExecutionGoals :many
SELECT goal_id FROM execution_goals WHERE execution_id = ? ORDER BY goal_id;

-- name: AddExecutionCapability :exec
INSERT INTO execution_capabilities (execution_id, capability_id) VALUES (?, ?);

-- name: ListExecutionCapabilities :many
SELECT capability_id FROM execution_capabilities WHERE execution_id = ? ORDER BY capability_id;

-- name: CreateExecutionTool :exec
INSERT INTO execution_tools (execution_id, name, permission, ordinal)
VALUES (?, ?, ?, ?);

-- name: ListExecutionTools :many
SELECT * FROM execution_tools WHERE execution_id = ? ORDER BY ordinal, name;

-- name: CreateExecutionEvent :exec
INSERT INTO execution_events (
    id, execution_id, from_status, to_status, event_kind,
    details_json, details_hash, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListExecutionEvents :many
SELECT * FROM execution_events WHERE execution_id = ? ORDER BY created_at, id;

-- name: CreateCandidateSet :exec
INSERT INTO candidate_sets (
    id, execution_id, attempt_id, candidate_hash, status,
    disposition_reason, created_at, disposed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetCandidateSet :one
SELECT * FROM candidate_sets WHERE id = ?;

-- name: GetCandidateSetByExecution :one
SELECT * FROM candidate_sets WHERE execution_id = ?;

-- name: GetCandidateSetByAttempt :one
SELECT * FROM candidate_sets WHERE attempt_id = ?;

-- name: GetLatestRejectedCandidateForResource :one
SELECT cs.* FROM candidate_sets cs
JOIN generation_attempts ga ON ga.id = cs.attempt_id
WHERE ga.session_id = ? AND ga.resource_id = ? AND cs.status = 'rejected'
ORDER BY cs.created_at DESC, cs.id DESC LIMIT 1;

-- name: UpdateCandidateSetDisposition :execrows
UPDATE candidate_sets
SET status = ?, disposition_reason = ?, disposed_at = ?
WHERE id = ? AND status = 'submitted';

-- name: CreateCandidateFile :exec
INSERT INTO candidate_files (
    id, candidate_id, path, content_blob, content_hash,
    byte_size, write_intent, ordinal
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListCandidateFiles :many
SELECT * FROM candidate_files WHERE candidate_id = ? ORDER BY ordinal, path;

-- name: AcceptCandidateFile :exec
INSERT INTO candidate_file_acceptances (
    candidate_file_id, resource_id, path, accepted_at
) VALUES (?, ?, ?, ?);

-- name: CreateFailureClassification :exec
INSERT INTO failure_classifications (
    id, attempt_id, execution_id, resource_id, category, origin, confidence,
    evidence_source, evidence_reference, evidence_blob, corrective_action,
    next_role, blocks_retry, override_of, resolution, resolved_by_attempt,
    created_at, resolved_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: AddFailureGoal :exec
INSERT INTO failure_goals (failure_id, goal_id) VALUES (?, ?);

-- name: GetFailureClassification :one
SELECT * FROM failure_classifications WHERE id = ?;

-- name: ListFailureClassificationsByAttempt :many
SELECT * FROM failure_classifications
WHERE attempt_id = ? ORDER BY created_at, id;

-- name: ListRecentFailureClassifications :many
SELECT * FROM failure_classifications
ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: ListFailureGoals :many
SELECT goal_id FROM failure_goals WHERE failure_id = ? ORDER BY goal_id;

-- name: ResolveFailureClassification :execrows
UPDATE failure_classifications
SET resolution = ?, resolved_by_attempt = ?, resolved_at = ?
WHERE id = ? AND resolved_at IS NULL;

-- name: CreateAttemptHandoff :exec
INSERT INTO attempt_handoffs (
    id, source_attempt_id, target_attempt_id, source_role, target_role,
    reason, expected_outcome, failure_id, status, created_at, accepted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetAttemptHandoff :one
SELECT * FROM attempt_handoffs WHERE id = ?;

-- name: ListAttemptHandoffsBySource :many
SELECT * FROM attempt_handoffs
WHERE source_attempt_id = ? ORDER BY created_at, id;

-- name: ListPendingAttemptHandoffs :many
SELECT * FROM attempt_handoffs
WHERE status = 'pending' ORDER BY created_at, id LIMIT ?;

-- name: AcceptAttemptHandoff :execrows
UPDATE attempt_handoffs
SET target_attempt_id = ?, status = 'accepted', accepted_at = ?
WHERE id = ? AND status = 'pending';
