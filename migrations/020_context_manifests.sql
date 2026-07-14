-- Phase 4: immutable, budgeted generation context and attempt provenance.
-- All served context content is snapshotted in SQLite; no sidecar manifests
-- are written into generated projects.

CREATE TABLE content_blobs (
    hash       TEXT PRIMARY KEY,
    content    BLOB NOT NULL,
    byte_size  INTEGER NOT NULL CHECK (byte_size >= 0),
    created_at TEXT NOT NULL
);

CREATE TABLE generation_attempts (
    id                TEXT PRIMARY KEY,
    session_id        TEXT NOT NULL REFERENCES agent_sessions(id),
    apply_id          TEXT NOT NULL REFERENCES applies(id),
    resource_id       TEXT NOT NULL,
    plan_operation_id TEXT NOT NULL DEFAULT '',
    parent_attempt_id TEXT REFERENCES generation_attempts(id),
    retry_number      INTEGER NOT NULL CHECK (retry_number >= 1),
    role              TEXT NOT NULL,
    status            TEXT NOT NULL CHECK (status IN (
                          'context_prepared','context_blocked','candidate_submitted',
                          'accepted','rejected','abandoned'
                      )),
    created_at        TEXT NOT NULL,
    UNIQUE (session_id, resource_id, retry_number)
);
CREATE INDEX idx_generation_attempts_session ON generation_attempts(session_id, created_at, id);
CREATE INDEX idx_generation_attempts_resource ON generation_attempts(resource_id, created_at, id);
CREATE INDEX idx_generation_attempts_parent ON generation_attempts(parent_attempt_id);

CREATE TABLE context_manifests (
    id                    TEXT PRIMARY KEY,
    attempt_id            TEXT NOT NULL UNIQUE REFERENCES generation_attempts(id),
    selector_version      TEXT NOT NULL,
    estimator_version     TEXT NOT NULL,
    selection_strategy    TEXT NOT NULL,
    budget_tokens         INTEGER NOT NULL CHECK (budget_tokens > 0),
    estimated_tokens      INTEGER NOT NULL CHECK (estimated_tokens >= 0),
    original_bytes        INTEGER NOT NULL CHECK (original_bytes >= 0),
    selected_bytes        INTEGER NOT NULL CHECK (selected_bytes >= 0),
    template_hashes_json  TEXT NOT NULL,
    system_prompt_blob    TEXT NOT NULL REFERENCES content_blobs(hash),
    rendered_prompt_blob  TEXT NOT NULL REFERENCES content_blobs(hash),
    context_hash          TEXT NOT NULL,
    blocked               INTEGER NOT NULL DEFAULT 0 CHECK (blocked IN (0,1)),
    blocked_reason        TEXT NOT NULL DEFAULT '',
    created_at            TEXT NOT NULL
);
CREATE INDEX idx_context_manifests_hash ON context_manifests(context_hash);
CREATE INDEX idx_context_manifests_created ON context_manifests(created_at, id);

CREATE TABLE context_sections (
    id               TEXT PRIMARY KEY,
    manifest_id      TEXT NOT NULL REFERENCES context_manifests(id) ON DELETE CASCADE,
    ordinal          INTEGER NOT NULL CHECK (ordinal >= 0),
    section_kind     TEXT NOT NULL,
    title            TEXT NOT NULL,
    source_kind      TEXT NOT NULL,
    source_id        TEXT NOT NULL,
    source_path      TEXT NOT NULL DEFAULT '',
    priority         INTEGER NOT NULL,
    mandatory        INTEGER NOT NULL CHECK (mandatory IN (0,1)),
    decision         TEXT NOT NULL CHECK (decision IN ('included','truncated','omitted')),
    reason           TEXT NOT NULL,
    original_hash    TEXT NOT NULL,
    content_blob     TEXT REFERENCES content_blobs(hash),
    original_bytes   INTEGER NOT NULL CHECK (original_bytes >= 0),
    selected_bytes   INTEGER NOT NULL CHECK (selected_bytes >= 0),
    estimated_tokens INTEGER NOT NULL CHECK (estimated_tokens >= 0),
    UNIQUE (manifest_id, ordinal)
);
CREATE INDEX idx_context_sections_manifest ON context_sections(manifest_id, ordinal);
CREATE INDEX idx_context_sections_source ON context_sections(source_kind, source_id);
