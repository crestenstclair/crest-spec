-- name: CreateJob :exec
INSERT INTO jobs (id, tool, status, pid, started_at)
VALUES (?, ?, 'running', ?, ?);

-- name: GetJob :one
SELECT * FROM jobs WHERE id = ?;

-- name: CompleteJob :execresult
UPDATE jobs SET status = 'completed', result = ?, done_at = ?
WHERE id = ? AND status = 'running';

-- name: FailJob :execresult
UPDATE jobs SET status = 'failed', error = ?, done_at = ?
WHERE id = ? AND status = 'running';

-- name: CancelJob :execresult
UPDATE jobs SET status = 'cancelled', done_at = ?
WHERE id = ? AND status = 'running';

-- name: DeleteJob :exec
UPDATE jobs SET status = 'deleted', done_at = ?
WHERE id = ?;

-- name: ListJobs :many
SELECT * FROM jobs
WHERE status != 'deleted'
ORDER BY started_at DESC
LIMIT ?;

-- name: ListRunningJobs :many
SELECT * FROM jobs WHERE status = 'running';

-- name: UpdateJobProgress :exec
UPDATE jobs SET progress_json = ?
WHERE id = ? AND status = 'running';
