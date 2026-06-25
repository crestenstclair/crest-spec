-- Behavioral verification pipeline.
-- designs hold bounded-context contracts; tasks are per-resource work items;
-- checks are falsification-gated behavioral claims; verifications are run records.

CREATE TABLE IF NOT EXISTS designs (
    context_name  TEXT PRIMARY KEY,
    spec_hash     TEXT NOT NULL DEFAULT '',
    contract_json TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tasks (
    id           TEXT PRIMARY KEY,
    resource_id  TEXT NOT NULL,
    context_name TEXT NOT NULL DEFAULT '',
    spec_hash    TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL DEFAULT '',
    state        TEXT NOT NULL DEFAULT 'pending'
                 CHECK (state IN ('pending','generated','verified','rejected','skipped')),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_resource_id ON tasks(resource_id);

CREATE TABLE IF NOT EXISTS checks (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    behavior    TEXT NOT NULL DEFAULT '',
    check_json  TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT 'pending'
                CHECK (state IN ('pending','passed','failed','theater','graduated')),
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_checks_task_id ON checks(task_id);
CREATE INDEX IF NOT EXISTS idx_checks_resource_id ON checks(resource_id);

CREATE TABLE IF NOT EXISTS verifications (
    id            TEXT PRIMARY KEY,
    check_id      TEXT NOT NULL,
    resource_id   TEXT NOT NULL,
    real_obs_json TEXT NOT NULL DEFAULT '',
    stub_obs_json TEXT NOT NULL DEFAULT '',
    passed        INTEGER NOT NULL DEFAULT 0,
    theater       INTEGER NOT NULL DEFAULT 0,
    reason        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_verifications_check_id ON verifications(check_id);
