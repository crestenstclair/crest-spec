-- name: CreateDispatchLifecycle :exec
INSERT INTO dispatch_lifecycles (
    id, session_id, resource_id, attempt_id, host_session_id, worker_id,
    dispatch_requested_at, worker_acknowledged_at, context_requested_at,
    context_prepared_at, execution_registered_at, candidate_submitted_at,
    candidate_accepted_at, candidate_rejected_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '', '', '', ?, ?);

-- name: CreateExplicitDispatchLifecycle :execrows
INSERT INTO dispatch_lifecycles (
    id, session_id, resource_id, attempt_id, host_session_id, worker_id,
    dispatch_requested_at, worker_acknowledged_at, context_requested_at,
    context_prepared_at, execution_registered_at, candidate_submitted_at,
    candidate_accepted_at, candidate_rejected_at, created_at, updated_at
)
SELECT sqlc.arg(id), sqlc.arg(session_id), sqlc.arg(resource_id), NULL,
       sqlc.arg(host_session_id), sqlc.arg(worker_id),
       sqlc.arg(dispatch_requested_at), '', '', '', '', '', '', '',
       sqlc.arg(created_at), sqlc.arg(updated_at)
WHERE NOT EXISTS (
    SELECT 1
    FROM dispatch_lifecycles AS other
    JOIN agent_sessions AS other_session ON other_session.id = other.session_id
    LEFT JOIN session_resources AS other_resource
      ON other_resource.session_id = other.session_id AND other_resource.resource_id = other.resource_id
    LEFT JOIN generation_attempts AS other_attempt ON other_attempt.id = other.attempt_id
    LEFT JOIN execution_manifests AS other_execution ON other_execution.attempt_id = other.attempt_id
    WHERE other.host_session_id = sqlc.arg(host_session_id)
      AND other.worker_id = sqlc.arg(worker_id)
      AND other_session.status = 'active'
      AND (other_resource.state IS NULL OR other_resource.state NOT IN ('committed','skipped'))
      AND other.candidate_accepted_at = ''
      AND other.candidate_rejected_at = ''
      AND (other_attempt.id IS NULL OR other_attempt.status NOT IN ('accepted','rejected','abandoned','context_blocked'))
      AND (other_execution.id IS NULL OR other_execution.status NOT IN ('accepted','rejected','completed','cancelled','failed','timed_out'))
);

-- name: GetDispatchLifecycle :one
SELECT * FROM dispatch_lifecycles WHERE id = ?;

-- name: GetDispatchLifecycleByAttempt :one
SELECT * FROM dispatch_lifecycles WHERE attempt_id = ?;

-- name: ListDispatchLifecyclesBySession :many
SELECT * FROM dispatch_lifecycles
WHERE session_id = ? ORDER BY created_at, id;

-- name: AcknowledgeDispatchLifecycle :execrows
UPDATE dispatch_lifecycles
SET worker_acknowledged_at = CASE
        WHEN worker_acknowledged_at = '' THEN sqlc.arg(acknowledged_at) ELSE worker_acknowledged_at END,
    host_session_id = CASE WHEN host_session_id = '' AND sqlc.arg(host_session_id) != '' THEN sqlc.arg(host_session_id) ELSE host_session_id END,
    worker_id = CASE WHEN worker_id = '' AND sqlc.arg(worker_id) != '' THEN sqlc.arg(worker_id) ELSE worker_id END,
    updated_at = sqlc.arg(updated_at)
WHERE dispatch_lifecycles.id = sqlc.arg(id)
  AND dispatch_lifecycles.session_id = sqlc.arg(session_id)
  AND dispatch_lifecycles.resource_id = sqlc.arg(resource_id)
  AND (sqlc.arg(host_session_id) = '' OR dispatch_lifecycles.host_session_id = '' OR dispatch_lifecycles.host_session_id = sqlc.arg(host_session_id))
  AND (sqlc.arg(worker_id) = '' OR dispatch_lifecycles.worker_id = '' OR dispatch_lifecycles.worker_id = sqlc.arg(worker_id))
  AND (dispatch_lifecycles.worker_acknowledged_at = ''
       OR (dispatch_lifecycles.host_session_id = '' AND sqlc.arg(host_session_id) != '')
       OR (dispatch_lifecycles.worker_id = '' AND sqlc.arg(worker_id) != ''))
  AND dispatch_lifecycles.attempt_id IS NOT NULL
  AND EXISTS (
      SELECT 1
      FROM generation_attempts AS attempt
      JOIN session_resources AS resource
        ON resource.session_id = attempt.session_id
       AND resource.resource_id = attempt.resource_id
      WHERE attempt.id = dispatch_lifecycles.attempt_id
        AND attempt.status = 'context_prepared'
        AND resource.state = 'dispatched'
        AND resource.job_id = attempt.id
  );

-- name: RequestExistingDispatchLifecycle :execrows
UPDATE dispatch_lifecycles
SET dispatch_requested_at = CASE
        WHEN dispatch_requested_at = '' THEN ? ELSE dispatch_requested_at END,
    host_session_id = CASE WHEN host_session_id = '' AND sqlc.arg(host_session_id) != '' THEN sqlc.arg(host_session_id) ELSE host_session_id END,
    worker_id = CASE WHEN worker_id = '' AND sqlc.arg(worker_id) != '' THEN sqlc.arg(worker_id) ELSE worker_id END,
    updated_at = sqlc.arg(updated_at)
WHERE dispatch_lifecycles.id = sqlc.arg(id)
  AND dispatch_lifecycles.session_id = sqlc.arg(session_id)
  AND dispatch_lifecycles.resource_id = sqlc.arg(resource_id)
  AND (sqlc.arg(host_session_id) = '' OR dispatch_lifecycles.host_session_id = '' OR dispatch_lifecycles.host_session_id = sqlc.arg(host_session_id))
  AND (sqlc.arg(worker_id) = '' OR dispatch_lifecycles.worker_id = '' OR dispatch_lifecycles.worker_id = sqlc.arg(worker_id))
  AND (dispatch_lifecycles.dispatch_requested_at = ''
       OR (dispatch_lifecycles.host_session_id = '' AND sqlc.arg(host_session_id) != '')
       OR (dispatch_lifecycles.worker_id = '' AND sqlc.arg(worker_id) != ''))
  AND NOT EXISTS (
      SELECT 1
      FROM dispatch_lifecycles AS other
      JOIN agent_sessions AS other_session ON other_session.id = other.session_id
      LEFT JOIN session_resources AS other_resource
        ON other_resource.session_id = other.session_id AND other_resource.resource_id = other.resource_id
      LEFT JOIN generation_attempts AS other_attempt ON other_attempt.id = other.attempt_id
      LEFT JOIN execution_manifests AS other_execution ON other_execution.attempt_id = other.attempt_id
      WHERE other.id <> dispatch_lifecycles.id
        AND other.host_session_id = sqlc.arg(host_session_id)
        AND other.worker_id = sqlc.arg(worker_id)
        AND other_session.status = 'active'
        AND (other_resource.state IS NULL OR other_resource.state NOT IN ('committed','skipped'))
        AND other.candidate_accepted_at = ''
        AND other.candidate_rejected_at = ''
        AND (other_attempt.id IS NULL OR other_attempt.status NOT IN ('accepted','rejected','abandoned','context_blocked'))
        AND (other_execution.id IS NULL OR other_execution.status NOT IN ('accepted','rejected','completed','cancelled','failed','timed_out'))
  );

-- name: RequestDispatchContext :execrows
UPDATE dispatch_lifecycles
SET context_requested_at = CASE
        WHEN context_requested_at = '' THEN ? ELSE context_requested_at END,
    updated_at = ?
WHERE id = ? AND session_id = ? AND resource_id = ? AND attempt_id IS NULL;

-- name: BindPreparedDispatchContext :execrows
UPDATE dispatch_lifecycles
SET attempt_id = ?, context_prepared_at = CASE
        WHEN context_prepared_at = '' THEN ? ELSE context_prepared_at END,
    updated_at = ?
WHERE id = ? AND session_id = ? AND resource_id = ?
  AND dispatch_requested_at = ''
  AND (attempt_id IS NULL OR attempt_id = ?);

-- name: MarkDispatchExecutionRegistered :execrows
UPDATE dispatch_lifecycles
SET execution_registered_at = CASE
        WHEN execution_registered_at = '' THEN sqlc.arg(execution_registered_at) ELSE execution_registered_at END,
    host_session_id = CASE WHEN host_session_id = '' AND sqlc.arg(host_session_id) != '' THEN sqlc.arg(host_session_id) ELSE host_session_id END,
    updated_at = sqlc.arg(updated_at)
WHERE attempt_id = sqlc.arg(attempt_id)
  AND (sqlc.arg(host_session_id) = '' OR host_session_id = '' OR host_session_id = sqlc.arg(host_session_id))
  AND (execution_registered_at = '' OR (host_session_id = '' AND sqlc.arg(host_session_id) != ''));

-- name: MarkDispatchCandidateSubmitted :execrows
UPDATE dispatch_lifecycles
SET candidate_submitted_at = CASE
        WHEN candidate_submitted_at = '' THEN ? ELSE candidate_submitted_at END,
    updated_at = ?
WHERE attempt_id = ?;

-- name: MarkDispatchCandidateAccepted :execrows
UPDATE dispatch_lifecycles
SET candidate_accepted_at = CASE
        WHEN candidate_accepted_at = '' THEN ? ELSE candidate_accepted_at END,
    updated_at = ?
WHERE attempt_id = ?;

-- name: MarkDispatchCandidateRejected :execrows
UPDATE dispatch_lifecycles
SET candidate_rejected_at = CASE
        WHEN candidate_rejected_at = '' THEN ? ELSE candidate_rejected_at END,
    updated_at = ?
WHERE attempt_id = ?;
