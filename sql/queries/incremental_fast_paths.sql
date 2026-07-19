-- name: GetReusableAcceptedCandidate :one
SELECT cs.*
FROM candidate_sets AS cs
JOIN generation_attempts AS ga ON ga.id = cs.attempt_id
JOIN agent_sessions AS sessions ON sessions.id = ga.session_id
WHERE ga.resource_id = ?
  AND cs.status = 'accepted'
  AND sessions.status = 'aborted'
  AND cs.generation_input_hash = ?
  AND cs.dependency_contract_hash = ?
ORDER BY cs.disposed_at DESC, cs.created_at DESC, cs.id DESC
LIMIT 1;

-- name: StampAcceptedCandidateInputs :execrows
UPDATE candidate_sets
SET generation_input_hash = ?, dependency_contract_hash = ?
WHERE id = ? AND status = 'accepted'
  AND generation_input_hash = '' AND dependency_contract_hash = '';

-- name: CreateCandidateReuse :exec
INSERT INTO candidate_reuses (
    id, session_id, resource_id, source_candidate_id, candidate_hash,
    generation_input_hash, dependency_contract_hash, reused_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: CommitReusedSessionResource :execrows
UPDATE session_resources
SET state = 'committed', phase = 'integration_pending', attempts = 0,
    last_error = NULL, last_output = NULL, job_id = NULL, updated_at = ?
WHERE session_id = ? AND resource_id = ? AND state = 'pending'
  AND execution_strategy = 'reuse_candidate' AND source_candidate_id = ?;

-- name: GetCandidateReuse :one
SELECT * FROM candidate_reuses WHERE session_id = ? AND resource_id = ?;

-- name: ListCandidateReusesBySession :many
SELECT * FROM candidate_reuses WHERE session_id = ? ORDER BY reused_at, resource_id;
