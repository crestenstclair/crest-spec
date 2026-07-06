CREATE TABLE resources (
    id               TEXT PRIMARY KEY,
    kind             TEXT NOT NULL,
    context_name     TEXT,
    declaration_hash TEXT NOT NULL,
    effective_hash   TEXT NOT NULL,
    model            TEXT,
    settled_at       TEXT NOT NULL
);

CREATE TABLE generated_files (
    path         TEXT PRIMARY KEY,
    resource_id  TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    content_hash TEXT NOT NULL,
    prompt_hash  TEXT NOT NULL,
    model        TEXT NOT NULL,
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_generated_files_resource ON generated_files(resource_id);

CREATE TABLE dependencies (
    source_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    kind      TEXT NOT NULL,
    PRIMARY KEY (source_id, target_id, kind)
);
