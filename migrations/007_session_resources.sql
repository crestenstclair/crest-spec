-- Per-resource state tracking within a session.
-- This is the state machine: every transition is recorded here.
-- The orchestrator reads this to know what to do next.
CREATE TABLE session_resources (
    session_id  TEXT NOT NULL REFERENCES agent_sessions(id),
    resource_id TEXT NOT NULL,
    state       TEXT NOT NULL DEFAULT 'pending'
                CHECK (state IN ('pending','dispatched','completed','committed','blocked','errored','timed_out','rejected','skipped')),
    wave_index  INTEGER NOT NULL DEFAULT 0,
    attempts    INTEGER NOT NULL DEFAULT 0,
    max_retries INTEGER NOT NULL DEFAULT 3,
    last_error  TEXT,
    last_output TEXT,
    job_id      TEXT,
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (session_id, resource_id)
);
CREATE INDEX idx_session_resources_session ON session_resources(session_id);
CREATE INDEX idx_session_resources_state ON session_resources(session_id, state);
