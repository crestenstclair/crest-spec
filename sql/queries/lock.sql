-- name: InsertLock :exec
INSERT INTO lock (id, holder, pid, acquired_at)
VALUES (1, ?, ?, ?);

-- name: DeleteLock :exec
DELETE FROM lock WHERE id = 1;

-- name: GetLock :one
SELECT * FROM lock WHERE id = 1;
