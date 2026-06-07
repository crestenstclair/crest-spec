-- name: CreateSession :exec
INSERT INTO agent_sessions (id, plan_json, waves_json, hashes_json, status, created_at, updated_at)
VALUES (?, ?, ?, ?, 'active', ?, ?);

-- name: GetSession :one
SELECT * FROM agent_sessions WHERE id = ?;

-- name: GetActiveSession :one
SELECT * FROM agent_sessions WHERE status = 'active' LIMIT 1;

-- name: UpdateSessionStatus :exec
UPDATE agent_sessions SET status = ?, current_wave = ?, updated_at = ?
WHERE id = ?;

-- name: CreateNote :exec
INSERT INTO agent_notes (resource_id, apply_id, content, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(resource_id, apply_id) DO UPDATE SET content = excluded.content, created_at = excluded.created_at;

-- name: GetNote :one
SELECT * FROM agent_notes WHERE resource_id = ? AND apply_id = ?;

-- name: ListNotes :many
SELECT * FROM agent_notes WHERE apply_id = ?;
