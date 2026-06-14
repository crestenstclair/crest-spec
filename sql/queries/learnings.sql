-- name: CreateLearning :exec
INSERT INTO learnings (id, scope_lang, scope_kind, text, rationale, source_generation_id, source_apply_id, confidence, status, times_applied, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListActiveLearnings :many
SELECT * FROM learnings
WHERE status = 'active'
  AND (scope_lang = '' OR scope_lang = ?)
  AND (scope_kind = '' OR scope_kind = ?)
ORDER BY confidence DESC, created_at DESC
LIMIT ?;

-- name: ListLearningsByStatus :many
SELECT * FROM learnings
WHERE status = ?
ORDER BY confidence DESC, created_at DESC;

-- name: UpdateLearningStatus :exec
UPDATE learnings SET status = ?, updated_at = ? WHERE id = ?;

-- name: IncrementLearningApplied :exec
UPDATE learnings SET times_applied = times_applied + 1, updated_at = ? WHERE id = ?;
