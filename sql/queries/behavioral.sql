-- name: UpsertDesign :exec
INSERT INTO designs (context_name, spec_hash, contract_json, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(context_name) DO UPDATE SET
    spec_hash     = excluded.spec_hash,
    contract_json = excluded.contract_json,
    updated_at    = excluded.updated_at;

-- name: GetDesign :one
SELECT * FROM designs WHERE context_name = ?;

-- name: ListDesigns :many
SELECT * FROM designs ORDER BY context_name ASC;

-- name: InsertTask :exec
INSERT INTO tasks (id, resource_id, context_name, spec_hash, description, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'pending', ?, ?);

-- name: GetTask :one
SELECT * FROM tasks WHERE id = ?;

-- name: ListTasksByResource :many
SELECT * FROM tasks WHERE resource_id = ? ORDER BY created_at ASC;

-- name: SetTaskState :exec
UPDATE tasks SET state = ?, updated_at = ? WHERE id = ?;

-- name: InsertCheck :exec
INSERT INTO checks (id, task_id, resource_id, behavior, check_json, state, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, 'pending', ?, ?);

-- name: GetCheck :one
SELECT * FROM checks WHERE id = ?;

-- name: ListChecksByTask :many
SELECT * FROM checks WHERE task_id = ? ORDER BY created_at ASC;

-- name: ListChecksByResource :many
SELECT * FROM checks WHERE resource_id = ? ORDER BY created_at ASC;

-- name: SetCheckState :exec
UPDATE checks SET state = ?, updated_at = ? WHERE id = ?;

-- name: InsertVerification :exec
INSERT INTO verifications (id, check_id, resource_id, real_obs_json, stub_obs_json, passed, theater, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListVerificationsByCheck :many
SELECT * FROM verifications WHERE check_id = ? ORDER BY created_at ASC;

-- name: DeleteChecksByResource :exec
DELETE FROM checks WHERE resource_id = ?;

-- name: DeleteTasksByResource :exec
DELETE FROM tasks WHERE resource_id = ?;
