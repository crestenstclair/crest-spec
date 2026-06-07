-- name: CreateGeneration :exec
INSERT INTO generations (id, apply_id, resource_id, prompt_text, prompt_hash, model, retry_count, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: UpdateGeneration :exec
UPDATE generations SET output_text = ?, outcome = ?, rejection_reason = ?,
    duration_ms = ?, input_tokens = ?, output_tokens = ?, cost_usd = ?
WHERE id = ?;

-- name: ListGenerations :many
SELECT * FROM generations WHERE resource_id = ?
ORDER BY created_at DESC LIMIT ?;

-- name: GetGeneration :one
SELECT * FROM generations WHERE id = ?;
