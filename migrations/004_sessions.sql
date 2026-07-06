CREATE TABLE agent_sessions (
    id           TEXT PRIMARY KEY,
    plan_json    TEXT NOT NULL,
    waves_json   TEXT NOT NULL,
    hashes_json  TEXT NOT NULL,
    current_wave INTEGER NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK (status IN ('active','completed','aborted')),
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE TABLE agent_notes (
    resource_id TEXT NOT NULL,
    apply_id    TEXT NOT NULL,
    content     TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    PRIMARY KEY (resource_id, apply_id)
);

CREATE TABLE lock (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    holder      TEXT NOT NULL,
    pid         INTEGER NOT NULL,
    acquired_at TEXT NOT NULL
);
