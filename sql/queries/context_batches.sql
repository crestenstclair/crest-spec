-- name: CreateContextBatch :exec
INSERT INTO context_batches (
    id, session_id, wave_index, status, started_at, created_at, updated_at
) VALUES (?, ?, ?, 'running', ?, ?, ?);

-- name: CompleteContextBatch :execrows
UPDATE context_batches
SET status = ?, completed_at = ?, updated_at = ?
WHERE id = ? AND status = 'running';

-- name: GetContextBatch :one
SELECT * FROM context_batches WHERE id = ?;

-- name: ListContextBatchesBySession :many
SELECT * FROM context_batches
WHERE session_id = ? ORDER BY started_at, id;

