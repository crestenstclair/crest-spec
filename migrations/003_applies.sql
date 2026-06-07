CREATE TABLE applies (
    id         TEXT PRIMARY KEY,
    status     TEXT NOT NULL DEFAULT 'running'
               CHECK (status IN ('running','completed','failed','cancelled')),
    spec_hash  TEXT NOT NULL,
    started_at TEXT NOT NULL,
    done_at    TEXT
);

CREATE TABLE apply_actions (
    id          TEXT PRIMARY KEY,
    apply_id    TEXT NOT NULL REFERENCES applies(id),
    resource_id TEXT NOT NULL,
    action      TEXT NOT NULL CHECK (action IN ('create','modify','destroy')),
    outcome     TEXT CHECK (outcome IN ('committed','rejected','skipped','errored')),
    error       TEXT,
    started_at  TEXT NOT NULL,
    done_at     TEXT
);
CREATE INDEX idx_apply_actions_apply ON apply_actions(apply_id);

CREATE TABLE generations (
    id               TEXT PRIMARY KEY,
    apply_id         TEXT REFERENCES applies(id),
    resource_id      TEXT NOT NULL,
    prompt_text      TEXT NOT NULL,
    prompt_hash      TEXT NOT NULL,
    output_text      TEXT,
    model            TEXT NOT NULL,
    outcome          TEXT CHECK (outcome IN ('accepted','rejected')),
    rejection_reason TEXT,
    retry_count      INTEGER NOT NULL DEFAULT 0,
    duration_ms      INTEGER,
    input_tokens     INTEGER,
    output_tokens    INTEGER,
    cost_usd         REAL,
    created_at       TEXT NOT NULL
);
CREATE INDEX idx_generations_resource ON generations(resource_id);
CREATE INDEX idx_generations_apply ON generations(apply_id);

CREATE TABLE invariant_checks (
    id          TEXT PRIMARY KEY,
    apply_id    TEXT NOT NULL REFERENCES applies(id),
    resource_id TEXT NOT NULL,
    invariant   TEXT NOT NULL,
    passed      INTEGER NOT NULL,
    details     TEXT,
    checked_at  TEXT NOT NULL
);
