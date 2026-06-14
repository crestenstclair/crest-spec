-- name: UpsertAmendment :exec
INSERT INTO amendments (id, resource_id, name, content_hash, origin, prompt, finding_json, validation_json, state, applied_spec_hash, created_at, applied_at, graduated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(resource_id, name) DO UPDATE SET
    content_hash      = excluded.content_hash,
    origin            = excluded.origin,
    prompt            = excluded.prompt,
    finding_json      = excluded.finding_json,
    validation_json   = excluded.validation_json,
    state             = excluded.state,
    applied_spec_hash = excluded.applied_spec_hash,
    applied_at        = excluded.applied_at,
    graduated_at      = excluded.graduated_at;

-- name: ListAmendmentsByResource :many
SELECT * FROM amendments WHERE resource_id = ? ORDER BY created_at ASC;

-- name: ListAmendmentsByState :many
SELECT * FROM amendments WHERE state = ? ORDER BY created_at ASC;

-- name: ListAllAmendments :many
SELECT * FROM amendments ORDER BY created_at ASC;

-- name: GetAmendment :one
SELECT * FROM amendments WHERE resource_id = ? AND name = ?;

-- name: UpdateAmendmentState :exec
UPDATE amendments SET state = ?, applied_spec_hash = ?, applied_at = ?, graduated_at = ? WHERE id = ?;

-- name: DeleteAmendment :exec
DELETE FROM amendments WHERE resource_id = ? AND name = ?;
