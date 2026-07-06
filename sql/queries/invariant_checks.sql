-- name: CreateInvariantCheck :exec
INSERT INTO invariant_checks (id, apply_id, resource_id, invariant, passed, details, checked_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListInvariantChecks :many
SELECT * FROM invariant_checks WHERE apply_id = ? ORDER BY checked_at;

-- name: ListInvariantChecksByResource :many
SELECT * FROM invariant_checks WHERE apply_id = ? AND resource_id = ? ORDER BY checked_at;
