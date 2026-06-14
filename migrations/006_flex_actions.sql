-- SQLite doesn't support ALTER CHECK constraints, so we recreate the table.
-- The old constraint was too narrow (only create/modify/destroy).
-- We need: create, modify, destroy, commit, validate, invariant, skip
CREATE TABLE apply_actions_new (
    id          TEXT PRIMARY KEY,
    apply_id    TEXT NOT NULL REFERENCES applies(id),
    resource_id TEXT NOT NULL,
    action      TEXT NOT NULL,
    outcome     TEXT,
    error       TEXT,
    started_at  TEXT NOT NULL,
    done_at     TEXT
);

INSERT INTO apply_actions_new SELECT * FROM apply_actions;
DROP TABLE apply_actions;
ALTER TABLE apply_actions_new RENAME TO apply_actions;
CREATE INDEX idx_apply_actions_apply ON apply_actions(apply_id);
