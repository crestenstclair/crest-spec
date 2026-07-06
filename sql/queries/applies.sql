-- name: CreateApply :exec
INSERT INTO applies (id, spec_hash, started_at)
VALUES (?, ?, ?);

-- name: GetApply :one
SELECT * FROM applies WHERE id = ?;

-- name: CompleteApply :execresult
UPDATE applies SET status = 'completed', done_at = ?
WHERE id = ? AND status = 'running';

-- name: FailApply :execresult
UPDATE applies SET status = 'failed', done_at = ?
WHERE id = ? AND status = 'running';

-- name: ListApplies :many
SELECT * FROM applies ORDER BY started_at DESC LIMIT ?;

-- name: CreateApplyAction :exec
INSERT INTO apply_actions (id, apply_id, resource_id, action, started_at)
VALUES (?, ?, ?, ?, ?);

-- name: UpdateApplyAction :exec
UPDATE apply_actions SET outcome = ?, error = ?, done_at = ?
WHERE id = ?;

-- name: ListApplyActions :many
SELECT * FROM apply_actions WHERE apply_id = ? ORDER BY started_at;
