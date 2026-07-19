-- name: UpsertSessionResource :exec
INSERT INTO session_resources (session_id, resource_id, state, wave_index, attempts, max_retries, last_error, last_output, job_id, updated_at, phase, dispatched_at, execution_strategy, source_candidate_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id, resource_id) DO UPDATE SET
    state = excluded.state,
    attempts = excluded.attempts,
    last_error = excluded.last_error,
    last_output = excluded.last_output,
    job_id = excluded.job_id,
    updated_at = excluded.updated_at,
    phase = excluded.phase,
    dispatched_at = excluded.dispatched_at,
    execution_strategy = excluded.execution_strategy,
    source_candidate_id = excluded.source_candidate_id;

-- name: GetSessionResource :one
SELECT * FROM session_resources WHERE session_id = ? AND resource_id = ?;

-- name: ListSessionResources :many
SELECT * FROM session_resources WHERE session_id = ? ORDER BY wave_index, resource_id;

-- name: ListSessionResourcesByState :many
SELECT * FROM session_resources WHERE session_id = ? AND state = ? ORDER BY wave_index, resource_id;

-- name: ListSessionResourcesByWave :many
SELECT * FROM session_resources WHERE session_id = ? AND wave_index = ? ORDER BY resource_id;

-- name: UpdateSessionResourceState :exec
UPDATE session_resources SET state = ?, last_error = ?, last_output = ?, attempts = ?, job_id = ?, updated_at = ?
WHERE session_id = ? AND resource_id = ?;

-- name: UpdateSessionResourcePhase :exec
UPDATE session_resources SET phase = ?, attempts = ?, updated_at = ?
WHERE session_id = ? AND resource_id = ?;

-- name: ClearSessionResourceExhaustionFailure :exec
UPDATE session_resources SET exhaustion_failure_id = NULL
WHERE session_id = ? AND resource_id = ?;

-- name: SetSessionResourceDispatched :exec
UPDATE session_resources SET state = 'dispatched', phase = 'queued', dispatched_at = ?, updated_at = ?
WHERE session_id = ? AND resource_id = ?;

-- name: DeleteSessionResources :exec
DELETE FROM session_resources WHERE session_id = ?;
