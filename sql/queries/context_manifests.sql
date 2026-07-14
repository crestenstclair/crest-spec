-- name: PutContentBlob :exec
INSERT INTO content_blobs (hash, content, byte_size, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(hash) DO NOTHING;

-- name: GetContentBlob :one
SELECT * FROM content_blobs WHERE hash = ?;

-- name: CountContentBlobs :one
SELECT COUNT(*) FROM content_blobs;

-- name: CountGenerationAttempts :one
SELECT COUNT(*) FROM generation_attempts
WHERE session_id = ? AND resource_id = ?;

-- name: CreateGenerationAttempt :exec
INSERT INTO generation_attempts (
    id, session_id, apply_id, resource_id, plan_operation_id,
    parent_attempt_id, retry_number, role, status, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetGenerationAttempt :one
SELECT * FROM generation_attempts WHERE id = ?;

-- name: GetLatestGenerationAttempt :one
SELECT * FROM generation_attempts
WHERE session_id = ? AND resource_id = ?
ORDER BY retry_number DESC LIMIT 1;

-- name: ListGenerationAttemptsBySession :many
SELECT * FROM generation_attempts
WHERE session_id = ? ORDER BY created_at, id;

-- name: ListGenerationAttemptsByResource :many
SELECT * FROM generation_attempts
WHERE resource_id = ? ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: ListRecentGenerationAttempts :many
SELECT * FROM generation_attempts
ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: CreateContextManifest :exec
INSERT INTO context_manifests (
    id, attempt_id, selector_version, estimator_version, selection_strategy,
    budget_tokens, estimated_tokens, original_bytes, selected_bytes,
    template_hashes_json, system_prompt_blob, rendered_prompt_blob,
    context_hash, blocked, blocked_reason, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetContextManifest :one
SELECT * FROM context_manifests WHERE id = ?;

-- name: GetContextManifestByAttempt :one
SELECT * FROM context_manifests WHERE attempt_id = ?;

-- name: ListRecentContextManifests :many
SELECT * FROM context_manifests ORDER BY created_at DESC, id DESC LIMIT ?;

-- name: ListContextManifestSummaries :many
SELECT
    cm.id, cm.attempt_id, cm.selector_version, cm.estimator_version,
    cm.selection_strategy, cm.budget_tokens, cm.estimated_tokens,
    cm.original_bytes, cm.selected_bytes, cm.context_hash, cm.blocked,
    cm.blocked_reason, cm.created_at,
    ga.session_id, ga.apply_id, ga.resource_id, ga.plan_operation_id,
    ga.parent_attempt_id, ga.retry_number, ga.role, ga.status
FROM context_manifests cm
JOIN generation_attempts ga ON ga.id = cm.attempt_id
ORDER BY cm.created_at DESC, cm.id DESC
LIMIT ?;

-- name: CreateContextSection :exec
INSERT INTO context_sections (
    id, manifest_id, ordinal, section_kind, title, source_kind, source_id,
    source_path, priority, mandatory, decision, reason, original_hash,
    content_blob, original_bytes, selected_bytes, estimated_tokens
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListContextSections :many
SELECT * FROM context_sections WHERE manifest_id = ? ORDER BY ordinal;

-- name: SetSessionResourceDispatchedTx :execrows
UPDATE session_resources
SET state = 'dispatched', phase = 'queued', dispatched_at = ?, updated_at = ?
WHERE session_id = ? AND resource_id = ?;
