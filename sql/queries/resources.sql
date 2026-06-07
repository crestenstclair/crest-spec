-- Resource state
-- name: GetResource :one
SELECT * FROM resources WHERE id = ?;

-- name: ListResources :many
SELECT * FROM resources ORDER BY id;

-- name: SetResource :exec
INSERT INTO resources (id, kind, context_name, declaration_hash, effective_hash, model, settled_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    kind = excluded.kind,
    context_name = excluded.context_name,
    declaration_hash = excluded.declaration_hash,
    effective_hash = excluded.effective_hash,
    model = excluded.model,
    settled_at = excluded.settled_at;

-- name: DeleteResource :exec
DELETE FROM resources WHERE id = ?;

-- Generated files
-- name: GetGeneratedFiles :many
SELECT * FROM generated_files WHERE resource_id = ?;

-- name: SetGeneratedFile :exec
INSERT INTO generated_files (path, resource_id, content_hash, prompt_hash, model, created_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
    resource_id = excluded.resource_id,
    content_hash = excluded.content_hash,
    prompt_hash = excluded.prompt_hash,
    model = excluded.model,
    created_at = excluded.created_at;

-- name: DeleteGeneratedFiles :exec
DELETE FROM generated_files WHERE resource_id = ?;

-- Dependencies
-- name: SetDependency :exec
INSERT INTO dependencies (source_id, target_id, kind)
VALUES (?, ?, ?)
ON CONFLICT(source_id, target_id, kind) DO NOTHING;

-- name: GetDependencies :many
SELECT * FROM dependencies WHERE source_id = ?;

-- name: DeleteDependencies :exec
DELETE FROM dependencies WHERE source_id = ?;
