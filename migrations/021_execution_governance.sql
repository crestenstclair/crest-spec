-- Phase 5: reproducible host execution, immutable candidates, role handoffs,
-- and deterministic failure classification. Operational evidence remains in
-- canonical SQLite state and reuses Phase 4 content-addressed blobs.

ALTER TABLE generation_attempts
ADD COLUMN role_policy_version TEXT NOT NULL DEFAULT 'role-policy-v1';

CREATE TABLE execution_manifests (
    id                       TEXT PRIMARY KEY,
    attempt_id               TEXT NOT NULL UNIQUE REFERENCES generation_attempts(id),
    context_manifest_id      TEXT NOT NULL UNIQUE REFERENCES context_manifests(id),
    protocol_version         TEXT NOT NULL,
    idempotency_key          TEXT NOT NULL UNIQUE,
    plan_operation_id        TEXT NOT NULL,
    role                     TEXT NOT NULL,
    role_policy_version      TEXT NOT NULL,
    context_policy           TEXT NOT NULL,
    host_name                TEXT NOT NULL,
    host_version             TEXT NOT NULL DEFAULT '',
    provider                 TEXT NOT NULL DEFAULT '',
    model                    TEXT NOT NULL,
    inference_config_json    TEXT NOT NULL,
    inference_config_hash    TEXT NOT NULL,
    agent_config_json        TEXT NOT NULL,
    agent_config_hash        TEXT NOT NULL,
    tool_permissions_json    TEXT NOT NULL,
    tool_permissions_hash    TEXT NOT NULL,
    template_hashes_json     TEXT NOT NULL,
    template_hashes_hash     TEXT NOT NULL,
    system_instructions_blob TEXT NOT NULL REFERENCES content_blobs(hash),
    context_hash             TEXT NOT NULL,
    host_session_id          TEXT NOT NULL DEFAULT '',
    status                   TEXT NOT NULL CHECK (status IN (
                                 'executing','candidate_submitted','validating',
                                 'accepted','rejected','completed','cancelled',
                                 'failed','timed_out'
                             )),
    disposition_reason       TEXT NOT NULL DEFAULT '',
    started_at               TEXT NOT NULL,
    completed_at             TEXT,
    duration_ms              INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    input_tokens             INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens            INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cost_usd                 REAL NOT NULL DEFAULT 0 CHECK (cost_usd >= 0),
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL
);
CREATE INDEX idx_execution_manifests_status ON execution_manifests(status, created_at, id);
CREATE INDEX idx_execution_manifests_host_model ON execution_manifests(host_name, model, created_at, id);
CREATE INDEX idx_execution_manifests_context_hash ON execution_manifests(context_hash);

CREATE TABLE execution_tools (
    execution_id TEXT NOT NULL REFERENCES execution_manifests(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    permission   TEXT NOT NULL,
    ordinal      INTEGER NOT NULL CHECK (ordinal >= 0),
    PRIMARY KEY (execution_id, name)
);
CREATE INDEX idx_execution_tools_execution ON execution_tools(execution_id, ordinal);

CREATE TABLE execution_events (
    id            TEXT PRIMARY KEY,
    execution_id  TEXT NOT NULL REFERENCES execution_manifests(id) ON DELETE CASCADE,
    from_status   TEXT NOT NULL DEFAULT '',
    to_status     TEXT NOT NULL,
    event_kind    TEXT NOT NULL,
    details_json  TEXT NOT NULL,
    details_hash  TEXT NOT NULL,
    created_at    TEXT NOT NULL
);
CREATE INDEX idx_execution_events_execution ON execution_events(execution_id, created_at, id);

CREATE TABLE candidate_sets (
    id                 TEXT PRIMARY KEY,
    execution_id       TEXT NOT NULL UNIQUE REFERENCES execution_manifests(id),
    attempt_id         TEXT NOT NULL UNIQUE REFERENCES generation_attempts(id),
    candidate_hash     TEXT NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('submitted','accepted','rejected')),
    disposition_reason TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    disposed_at        TEXT
);
CREATE INDEX idx_candidate_sets_hash ON candidate_sets(candidate_hash);

CREATE TABLE candidate_files (
    id            TEXT PRIMARY KEY,
    candidate_id  TEXT NOT NULL REFERENCES candidate_sets(id) ON DELETE CASCADE,
    path          TEXT NOT NULL,
    content_blob  TEXT NOT NULL REFERENCES content_blobs(hash),
    content_hash  TEXT NOT NULL,
    byte_size     INTEGER NOT NULL CHECK (byte_size >= 0),
    write_intent  TEXT NOT NULL CHECK (write_intent IN ('create','modify','preserve')),
    ordinal       INTEGER NOT NULL CHECK (ordinal >= 0),
    UNIQUE (candidate_id, path)
);
CREATE INDEX idx_candidate_files_candidate ON candidate_files(candidate_id, ordinal);
CREATE INDEX idx_candidate_files_path ON candidate_files(path);

CREATE TABLE candidate_file_acceptances (
    candidate_file_id TEXT PRIMARY KEY REFERENCES candidate_files(id) ON DELETE CASCADE,
    resource_id       TEXT NOT NULL,
    path              TEXT NOT NULL,
    accepted_at       TEXT NOT NULL
);
CREATE INDEX idx_candidate_acceptances_resource ON candidate_file_acceptances(resource_id, path);

CREATE TABLE failure_classifications (
    id                    TEXT PRIMARY KEY,
    attempt_id            TEXT NOT NULL REFERENCES generation_attempts(id),
    execution_id          TEXT REFERENCES execution_manifests(id),
    resource_id           TEXT NOT NULL,
    category              TEXT NOT NULL CHECK (category IN (
                                  'missing_project_intent','missing_context',
                                  'incorrect_context_selection','stale_context',
                                  'context_truncation','ambiguous_resource_contract',
                                  'architectural_inconsistency','implementation_error',
                                  'integration_error','invalid_validation',
                                  'behavioral_theater','tool_failure','host_failure',
                                  'model_failure','unsupported_project_pattern'
                              )),
    origin                TEXT NOT NULL CHECK (origin IN ('engine','host','human')),
    confidence            REAL NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    evidence_source       TEXT NOT NULL,
    evidence_reference    TEXT NOT NULL DEFAULT '',
    evidence_blob         TEXT REFERENCES content_blobs(hash),
    corrective_action     TEXT NOT NULL,
    next_role             TEXT NOT NULL DEFAULT '',
    blocks_retry          INTEGER NOT NULL CHECK (blocks_retry IN (0,1)),
    override_of           TEXT REFERENCES failure_classifications(id),
    resolution            TEXT NOT NULL DEFAULT '',
    resolved_by_attempt   TEXT REFERENCES generation_attempts(id),
    created_at            TEXT NOT NULL,
    resolved_at           TEXT
);
CREATE INDEX idx_failure_classifications_attempt ON failure_classifications(attempt_id, created_at, id);
CREATE INDEX idx_failure_classifications_category ON failure_classifications(category, created_at, id);
CREATE INDEX idx_failure_classifications_resource ON failure_classifications(resource_id, created_at, id);

CREATE TABLE failure_goals (
    failure_id TEXT NOT NULL REFERENCES failure_classifications(id) ON DELETE CASCADE,
    goal_id    TEXT NOT NULL REFERENCES project_goals(id) ON DELETE CASCADE,
    PRIMARY KEY (failure_id, goal_id)
);

CREATE TABLE attempt_handoffs (
    id                  TEXT PRIMARY KEY,
    source_attempt_id   TEXT NOT NULL REFERENCES generation_attempts(id),
    target_attempt_id   TEXT REFERENCES generation_attempts(id),
    source_role         TEXT NOT NULL,
    target_role         TEXT NOT NULL,
    reason              TEXT NOT NULL,
    expected_outcome    TEXT NOT NULL,
    failure_id          TEXT REFERENCES failure_classifications(id),
    status              TEXT NOT NULL CHECK (status IN ('pending','accepted','cancelled')),
    created_at          TEXT NOT NULL,
    accepted_at         TEXT
);
CREATE INDEX idx_attempt_handoffs_source ON attempt_handoffs(source_attempt_id, created_at, id);
CREATE INDEX idx_attempt_handoffs_target ON attempt_handoffs(target_attempt_id);
CREATE INDEX idx_attempt_handoffs_status ON attempt_handoffs(status, created_at, id);
