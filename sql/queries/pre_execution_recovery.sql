-- name: ListStalePreExecutionAttemptIDs :many
SELECT ga.id
FROM generation_attempts AS ga
JOIN agent_sessions AS session ON session.id = ga.session_id
JOIN session_resources AS resource
  ON resource.session_id = ga.session_id
 AND resource.resource_id = ga.resource_id
LEFT JOIN dispatch_lifecycles AS lifecycle ON lifecycle.attempt_id = ga.id
WHERE ga.status = 'context_prepared'
  AND session.status = 'active'
  AND NOT EXISTS (
      SELECT 1 FROM execution_manifests AS execution
      WHERE execution.attempt_id = ga.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM candidate_sets AS candidate
      WHERE candidate.attempt_id = ga.id
  )
  AND COALESCE(NULLIF(lifecycle.updated_at, ''), ga.created_at) < ?
ORDER BY COALESCE(NULLIF(lifecycle.updated_at, ''), ga.created_at), ga.id;

-- name: GetRecoverablePreExecutionAttempt :one
SELECT
    ga.id AS attempt_id,
    ga.session_id,
    ga.resource_id,
    ga.status AS attempt_status,
    resource.state AS resource_state,
    COALESCE(resource.job_id, '') AS resource_job_id,
    COALESCE(lifecycle.id, '') AS dispatch_lifecycle_id,
    COALESCE(NULLIF(lifecycle.updated_at, ''), ga.created_at) AS last_activity_at
FROM generation_attempts AS ga
JOIN session_resources AS resource
  ON resource.session_id = ga.session_id
 AND resource.resource_id = ga.resource_id
LEFT JOIN dispatch_lifecycles AS lifecycle ON lifecycle.attempt_id = ga.id
WHERE ga.id = ?
  AND ga.status = 'context_prepared'
  AND NOT EXISTS (
      SELECT 1 FROM execution_manifests AS execution
      WHERE execution.attempt_id = ga.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM candidate_sets AS candidate
      WHERE candidate.attempt_id = ga.id
  );

-- name: AbandonRecoverablePreExecutionAttempt :execrows
UPDATE generation_attempts
SET status = 'abandoned'
WHERE generation_attempts.id = ?
  AND generation_attempts.status = 'context_prepared'
  AND NOT EXISTS (
      SELECT 1 FROM execution_manifests AS execution
      WHERE execution.attempt_id = generation_attempts.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM candidate_sets AS candidate
      WHERE candidate.attempt_id = generation_attempts.id
  )
  AND EXISTS (
      SELECT 1
      FROM session_resources AS resource
      WHERE resource.session_id = generation_attempts.session_id
        AND resource.resource_id = generation_attempts.resource_id
  );

-- name: AbandonFailedDispatchAttempt :execrows
UPDATE generation_attempts
SET status = 'abandoned'
WHERE generation_attempts.id = sqlc.arg(attempt_id)
  AND generation_attempts.status = 'context_prepared'
  AND EXISTS (
      SELECT 1
      FROM dispatch_lifecycles AS lifecycle
      JOIN session_resources AS resource
        ON resource.session_id = lifecycle.session_id
       AND resource.resource_id = lifecycle.resource_id
      WHERE lifecycle.id = sqlc.arg(dispatch_id)
        AND lifecycle.attempt_id = generation_attempts.id
        AND lifecycle.session_id = sqlc.arg(session_id)
        AND lifecycle.resource_id = sqlc.arg(resource_id)
        AND lifecycle.host_session_id = sqlc.arg(host_session_id)
        AND lifecycle.worker_id = sqlc.arg(worker_id)
        AND lifecycle.dispatch_requested_at != ''
        AND lifecycle.execution_registered_at = ''
        AND resource.state = 'dispatched'
        AND resource.job_id = generation_attempts.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM execution_manifests AS execution
      WHERE execution.attempt_id = generation_attempts.id
  )
  AND NOT EXISTS (
      SELECT 1 FROM candidate_sets AS candidate
      WHERE candidate.attempt_id = generation_attempts.id
  );

-- name: GetDispatchFailureAuditID :one
SELECT id
FROM failure_classifications
WHERE attempt_id = sqlc.arg(attempt_id)
  AND origin = 'engine'
  AND category = 'stale_context'
  AND evidence_source = 'host_dispatch_failure'
  AND evidence_reference = sqlc.arg(dispatch_id)
ORDER BY created_at, id
LIMIT 1;

-- name: ReleaseRecoveredPreExecutionClaim :execrows
UPDATE session_resources
SET state = 'pending',
    phase = 'pre_execution_recovered',
    last_error = ?,
    last_output = NULL,
    attempts = (
        SELECT COUNT(*)
        FROM execution_manifests AS execution
        JOIN generation_attempts AS counted_attempt
          ON counted_attempt.id = execution.attempt_id
        WHERE counted_attempt.session_id = session_resources.session_id
          AND counted_attempt.resource_id = session_resources.resource_id
          AND counted_attempt.retry_number > COALESCE((
              SELECT MAX(accepted_attempt.retry_number)
              FROM generation_attempts AS accepted_attempt
              WHERE accepted_attempt.session_id = session_resources.session_id
                AND accepted_attempt.resource_id = session_resources.resource_id
                AND accepted_attempt.status = 'accepted'
          ), 0)
          AND NOT EXISTS (
              SELECT 1
              FROM failure_classifications AS failure
              WHERE failure.attempt_id = counted_attempt.id
                AND failure.origin = 'engine'
                AND failure.category = 'stale_context'
          )
    ),
    job_id = NULL,
    updated_at = ?
WHERE session_resources.session_id = ?
  AND session_resources.resource_id = ?
  AND session_resources.state = 'dispatched'
  AND session_resources.job_id = ?;

-- name: CountExecutionAttemptsBySessionResource :one
SELECT COUNT(*)
FROM execution_manifests AS execution
JOIN generation_attempts AS attempt ON attempt.id = execution.attempt_id
WHERE attempt.session_id = sqlc.arg(input_session_id)
  AND attempt.resource_id = sqlc.arg(input_resource_id)
  AND NOT EXISTS (
      SELECT 1
      FROM failure_classifications AS failure
      WHERE failure.attempt_id = attempt.id
        AND failure.origin = 'engine'
        AND failure.category = 'stale_context'
  );

-- name: CountRepairEpochExecutionAttemptsBySessionResource :one
SELECT COUNT(*)
FROM execution_manifests AS execution
JOIN generation_attempts AS attempt ON attempt.id = execution.attempt_id
WHERE attempt.session_id = sqlc.arg(input_session_id)
  AND attempt.resource_id = sqlc.arg(input_resource_id)
  AND attempt.retry_number > COALESCE((
      SELECT MAX(accepted_attempt.retry_number)
      FROM generation_attempts AS accepted_attempt
      WHERE accepted_attempt.session_id = sqlc.arg(input_session_id)
        AND accepted_attempt.resource_id = sqlc.arg(input_resource_id)
        AND accepted_attempt.status = 'accepted'
  ), 0)
  AND NOT EXISTS (
      SELECT 1
      FROM failure_classifications AS failure
      WHERE failure.attempt_id = attempt.id
        AND failure.origin = 'engine'
        AND failure.category = 'stale_context'
  );

-- name: CountAffectedClosureRepairCyclesBySessionResource :one
SELECT COUNT(*)
FROM failure_classifications AS failure
JOIN generation_attempts AS attempt ON attempt.id = failure.attempt_id
WHERE attempt.session_id = sqlc.arg(input_session_id)
  AND attempt.resource_id = sqlc.arg(input_resource_id)
  AND failure.resource_id = attempt.resource_id
  AND failure.category = 'integration_error'
  AND failure.origin = 'engine'
  AND failure.evidence_source = 'affected_closure_validation'
  AND (failure.resolved_at IS NULL OR failure.resolved_by_attempt IS NOT NULL);

-- name: MaxAffectedClosureRepairCyclesBySession :one
SELECT CAST(COALESCE(MAX(resource_cycles.retry_count), 0) AS INTEGER)
FROM (
    SELECT failure.resource_id, COUNT(*) AS retry_count
    FROM failure_classifications AS failure
    JOIN generation_attempts AS attempt ON attempt.id = failure.attempt_id
    WHERE attempt.session_id = sqlc.arg(input_session_id)
      AND failure.resource_id = attempt.resource_id
      AND failure.category = 'integration_error'
      AND failure.origin = 'engine'
      AND failure.evidence_source = 'affected_closure_validation'
      AND (failure.resolved_at IS NULL OR failure.resolved_by_attempt IS NOT NULL)
    GROUP BY failure.resource_id
) AS resource_cycles;

-- name: CountExecutionAttemptsThroughAttempt :one
SELECT COUNT(*)
FROM execution_manifests AS execution
JOIN generation_attempts AS counted_attempt ON counted_attempt.id = execution.attempt_id
JOIN generation_attempts AS target_attempt ON target_attempt.id = sqlc.arg(target_attempt_id)
WHERE counted_attempt.session_id = target_attempt.session_id
  AND counted_attempt.resource_id = target_attempt.resource_id
  AND counted_attempt.retry_number <= target_attempt.retry_number
  AND NOT EXISTS (
      SELECT 1
      FROM failure_classifications AS failure
      WHERE failure.attempt_id = counted_attempt.id
        AND failure.origin = 'engine'
        AND failure.category = 'stale_context'
  );

-- name: ReleaseTerminalExecutionClaim :execrows
UPDATE session_resources
SET state = sqlc.arg(next_state),
    phase = sqlc.arg(next_phase),
    last_error = sqlc.arg(last_error),
    last_output = NULL,
    attempts = (
        SELECT COUNT(*)
        FROM execution_manifests AS execution
        JOIN generation_attempts AS counted_attempt
          ON counted_attempt.id = execution.attempt_id
        WHERE counted_attempt.session_id = session_resources.session_id
          AND counted_attempt.resource_id = session_resources.resource_id
          AND counted_attempt.retry_number > COALESCE((
              SELECT MAX(accepted_attempt.retry_number)
              FROM generation_attempts AS accepted_attempt
              WHERE accepted_attempt.session_id = session_resources.session_id
                AND accepted_attempt.resource_id = session_resources.resource_id
                AND accepted_attempt.status = 'accepted'
          ), 0)
          AND NOT EXISTS (
              SELECT 1
              FROM failure_classifications AS failure
              WHERE failure.attempt_id = counted_attempt.id
                AND failure.origin = 'engine'
                AND failure.category = 'stale_context'
          )
    ),
    job_id = NULL,
    updated_at = sqlc.arg(updated_at)
WHERE session_resources.session_id = sqlc.arg(input_session_id)
  AND session_resources.resource_id = sqlc.arg(input_resource_id)
  AND session_resources.state = 'dispatched'
  AND session_resources.job_id = sqlc.arg(attempt_id);
