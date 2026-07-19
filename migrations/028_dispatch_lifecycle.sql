-- Durable host/engine hand-off timestamps. A lifecycle may begin before an
-- attempt exists, so attempt_id is bound only after context preparation.
-- Empty timestamps mean "not observed"; they must never be inferred.
CREATE TABLE dispatch_lifecycles (
    id                       TEXT PRIMARY KEY,
    session_id               TEXT NOT NULL REFERENCES agent_sessions(id),
    resource_id              TEXT NOT NULL,
    attempt_id               TEXT UNIQUE REFERENCES generation_attempts(id),
    host_session_id          TEXT NOT NULL DEFAULT '',
    worker_id                TEXT NOT NULL DEFAULT '',
    dispatch_requested_at    TEXT NOT NULL DEFAULT '',
    worker_acknowledged_at   TEXT NOT NULL DEFAULT '',
    context_requested_at     TEXT NOT NULL DEFAULT '',
    context_prepared_at      TEXT NOT NULL DEFAULT '',
    execution_registered_at TEXT NOT NULL DEFAULT '',
    candidate_submitted_at   TEXT NOT NULL DEFAULT '',
    candidate_accepted_at    TEXT NOT NULL DEFAULT '',
    candidate_rejected_at    TEXT NOT NULL DEFAULT '',
    created_at               TEXT NOT NULL,
    updated_at               TEXT NOT NULL
);

CREATE INDEX idx_dispatch_lifecycles_session
    ON dispatch_lifecycles(session_id, created_at, id);
CREATE INDEX idx_dispatch_lifecycles_resource
    ON dispatch_lifecycles(session_id, resource_id, created_at, id);
CREATE INDEX idx_dispatch_lifecycles_worker
    ON dispatch_lifecycles(host_session_id, worker_id);
