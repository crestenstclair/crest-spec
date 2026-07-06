-- Add a 'malformed' state to checks: a structurally invalid predicate (e.g.
-- eq on a non-numeric field with no member to compare against) can never
-- pass regardless of how the implementation is regenerated. Distinguishing
-- it from 'failed' lets the orchestrator quarantine the check instead of
-- cycling it through the verify->regenerate loop forever.
--
-- SQLite cannot ALTER a CHECK constraint in place, so the table is rebuilt.

CREATE TABLE checks_new (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    behavior    TEXT NOT NULL DEFAULT '',
    check_json  TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT 'pending'
                CHECK (state IN ('pending','passed','failed','theater','graduated','malformed')),
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

INSERT INTO checks_new (id, task_id, resource_id, behavior, check_json, state, created_at, updated_at)
SELECT id, task_id, resource_id, behavior, check_json, state, created_at, updated_at FROM checks;

DROP TABLE checks;

ALTER TABLE checks_new RENAME TO checks;

CREATE INDEX IF NOT EXISTS idx_checks_task_id ON checks(task_id);
CREATE INDEX IF NOT EXISTS idx_checks_resource_id ON checks(resource_id);
