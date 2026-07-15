-- Phase 6: executable validation and behavioral-evidence provenance.
-- Definitions are immutable snapshots reconciled from CUE. Operational runs,
-- command output, parsed observations, predicate decisions, and evidence
-- currency remain exclusively in SQLite.

CREATE TABLE verification_definitions (
    snapshot_id     TEXT PRIMARY KEY,
    definition_id   TEXT NOT NULL,
    project_name    TEXT NOT NULL REFERENCES project_state(project_name),
    definition_type TEXT NOT NULL CHECK (definition_type IN ('validation','witness')),
    scope           TEXT NOT NULL CHECK (scope IN (
                        'resource','dependency_contract','integration_wave',
                        'goal','project','regression','behavioral'
                    )),
    kind            TEXT NOT NULL,
    definition_hash TEXT NOT NULL,
    definition_json TEXT NOT NULL,
    spec_hash       TEXT NOT NULL,
    active          INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (project_name, definition_id, definition_hash)
);
CREATE INDEX idx_verification_definitions_current
    ON verification_definitions(project_name, active, definition_type, scope, definition_id);

CREATE TABLE verification_definition_targets (
    snapshot_id TEXT NOT NULL REFERENCES verification_definitions(snapshot_id) ON DELETE CASCADE,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('resource','capability','goal','evidence','completion_check')),
    target_id   TEXT NOT NULL,
    PRIMARY KEY (snapshot_id, target_kind, target_id)
);
CREATE INDEX idx_verification_definition_targets_target
    ON verification_definition_targets(target_kind, target_id, snapshot_id);

CREATE TABLE validation_runs (
    id                     TEXT PRIMARY KEY,
    definition_snapshot_id TEXT NOT NULL REFERENCES verification_definitions(snapshot_id),
    definition_id          TEXT NOT NULL,
    session_id             TEXT REFERENCES agent_sessions(id),
    attempt_id             TEXT REFERENCES generation_attempts(id),
    execution_id           TEXT REFERENCES execution_manifests(id),
    source_tree_hash       TEXT NOT NULL,
    classification         TEXT NOT NULL CHECK (classification IN (
                               'passed','failed','malformed','theater','error'
                           )),
    reason                 TEXT NOT NULL DEFAULT '',
    started_at             TEXT NOT NULL,
    completed_at           TEXT NOT NULL,
    duration_ms            INTEGER NOT NULL CHECK (duration_ms >= 0),
    created_at             TEXT NOT NULL
);
CREATE INDEX idx_validation_runs_definition
    ON validation_runs(definition_id, created_at DESC, id);
CREATE INDEX idx_validation_runs_session
    ON validation_runs(session_id, created_at DESC, id);
CREATE INDEX idx_validation_runs_attempt
    ON validation_runs(attempt_id, created_at DESC, id);

CREATE TABLE validation_run_targets (
    run_id      TEXT NOT NULL REFERENCES validation_runs(id) ON DELETE CASCADE,
    target_kind TEXT NOT NULL CHECK (target_kind IN ('resource','capability','goal','evidence')),
    target_id   TEXT NOT NULL,
    PRIMARY KEY (run_id, target_kind, target_id)
);
CREATE INDEX idx_validation_run_targets_target
    ON validation_run_targets(target_kind, target_id, run_id);

CREATE TABLE validation_executions (
    id                TEXT PRIMARY KEY,
    run_id            TEXT NOT NULL REFERENCES validation_runs(id) ON DELETE CASCADE,
    execution_role    TEXT NOT NULL CHECK (execution_role IN ('validation','real','negative')),
    command_json      TEXT NOT NULL,
    command_hash      TEXT NOT NULL,
    working_directory TEXT NOT NULL,
    environment_json  TEXT NOT NULL,
    environment_hash  TEXT NOT NULL,
    source_tree_hash  TEXT NOT NULL,
    executable_hash   TEXT NOT NULL DEFAULT '',
    stdout_blob       TEXT NOT NULL REFERENCES content_blobs(hash),
    stderr_blob       TEXT NOT NULL REFERENCES content_blobs(hash),
    stdout_hash       TEXT NOT NULL,
    stderr_hash       TEXT NOT NULL,
    stdout_bytes      INTEGER NOT NULL CHECK (stdout_bytes >= 0),
    stderr_bytes      INTEGER NOT NULL CHECK (stderr_bytes >= 0),
    stdout_truncated  INTEGER NOT NULL DEFAULT 0 CHECK (stdout_truncated IN (0,1)),
    stderr_truncated  INTEGER NOT NULL DEFAULT 0 CHECK (stderr_truncated IN (0,1)),
    exit_code         INTEGER NOT NULL,
    timed_out         INTEGER NOT NULL DEFAULT 0 CHECK (timed_out IN (0,1)),
    launch_error      TEXT NOT NULL DEFAULT '',
    started_at        TEXT NOT NULL,
    completed_at      TEXT NOT NULL,
    duration_ms       INTEGER NOT NULL CHECK (duration_ms >= 0),
    UNIQUE (run_id, execution_role)
);
CREATE INDEX idx_validation_executions_command
    ON validation_executions(command_hash, started_at);

CREATE TABLE validation_artifacts (
    execution_id TEXT NOT NULL REFERENCES validation_executions(id) ON DELETE CASCADE,
    path         TEXT NOT NULL,
    content_blob TEXT REFERENCES content_blobs(hash),
    content_hash TEXT NOT NULL DEFAULT '',
    byte_size    INTEGER NOT NULL DEFAULT 0 CHECK (byte_size >= 0),
    captured_bytes INTEGER NOT NULL DEFAULT 0 CHECK (captured_bytes >= 0),
    truncated    INTEGER NOT NULL DEFAULT 0 CHECK (truncated IN (0,1)),
    missing      INTEGER NOT NULL DEFAULT 0 CHECK (missing IN (0,1)),
    PRIMARY KEY (execution_id, path)
);

CREATE TABLE parsed_observations (
    execution_id    TEXT PRIMARY KEY REFERENCES validation_executions(id) ON DELETE CASCADE,
    schema_json     TEXT NOT NULL,
    schema_hash     TEXT NOT NULL,
    observation_json TEXT NOT NULL,
    observation_hash TEXT NOT NULL,
    parse_error     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE validation_predicate_results (
    run_id         TEXT NOT NULL REFERENCES validation_runs(id) ON DELETE CASCADE,
    execution_id   TEXT NOT NULL REFERENCES validation_executions(id) ON DELETE CASCADE,
    ordinal        INTEGER NOT NULL CHECK (ordinal >= 0),
    field_name     TEXT NOT NULL,
    operator       TEXT NOT NULL,
    expected_json  TEXT NOT NULL,
    observed_json  TEXT NOT NULL,
    passed         INTEGER NOT NULL CHECK (passed IN (0,1)),
    reason         TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (run_id, execution_id, ordinal)
);

CREATE TABLE verification_evidence_records (
    id                TEXT PRIMARY KEY,
    run_id            TEXT NOT NULL REFERENCES validation_runs(id),
    evidence_id       TEXT NOT NULL REFERENCES evidence_requirements(id),
    source_tree_hash  TEXT NOT NULL,
    definition_hash   TEXT NOT NULL,
    classification    TEXT NOT NULL CHECK (classification IN ('passed','failed','malformed','theater','error')),
    currency          TEXT NOT NULL DEFAULT 'current' CHECK (currency IN ('current','stale')),
    created_at        TEXT NOT NULL,
    invalidated_at    TEXT,
    UNIQUE (run_id, evidence_id)
);
CREATE INDEX idx_verification_evidence_current
    ON verification_evidence_records(evidence_id, currency, created_at DESC, id);

CREATE TABLE verification_evidence_invalidations (
    id                 TEXT PRIMARY KEY,
    evidence_record_id TEXT NOT NULL REFERENCES verification_evidence_records(id),
    reason             TEXT NOT NULL,
    source_type        TEXT NOT NULL,
    source_id          TEXT NOT NULL,
    created_at         TEXT NOT NULL
);
CREATE INDEX idx_verification_evidence_invalidations_record
    ON verification_evidence_invalidations(evidence_record_id, created_at, id);
