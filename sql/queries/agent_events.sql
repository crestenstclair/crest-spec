-- name: CreateAgentEvent :exec
INSERT INTO agent_events (id, generation_id, resource_id, apply_id, event_type, attempt, content, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAgentEventsByGeneration :many
SELECT * FROM agent_events
WHERE generation_id = ?
ORDER BY created_at ASC;

-- name: ListAgentEventsByResource :many
SELECT * FROM agent_events
WHERE resource_id = ?
ORDER BY created_at ASC;

-- name: ListAgentEventsByApply :many
SELECT * FROM agent_events
WHERE apply_id = ?
ORDER BY created_at DESC
LIMIT ?;

-- name: ListRecentAgentEvents :many
SELECT * FROM agent_events
ORDER BY created_at DESC
LIMIT ?;

-- name: DeleteAgentEventsBefore :exec
DELETE FROM agent_events WHERE created_at < ?;
