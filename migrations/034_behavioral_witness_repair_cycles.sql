-- A failed behavioral witness may authorize narrowly declared source repair,
-- but that authorization must not become an unbounded verify/regenerate loop.
-- Each row is one durable routing decision for an exact witness definition and
-- exact source identity.  A changed definition or genuinely changed accepted
-- candidate forms a new epoch; replaying the same candidate/definition does
-- not erase prior repair charges.
CREATE TABLE behavioral_witness_repair_cycles (
    id                     TEXT PRIMARY KEY,
    session_id             TEXT NOT NULL REFERENCES agent_sessions(id),
    resource_id            TEXT NOT NULL,
    validation_run_id      TEXT NOT NULL REFERENCES validation_runs(id),
    definition_snapshot_id TEXT NOT NULL REFERENCES verification_definitions(snapshot_id),
    definition_hash        TEXT NOT NULL,
    source_kind            TEXT NOT NULL CHECK (source_kind IN (
                               'candidate','owned_files','source_tree'
                           )),
    source_hash            TEXT NOT NULL,
    retry_number           INTEGER NOT NULL CHECK (retry_number > 0),
    max_retries            INTEGER NOT NULL CHECK (max_retries >= 0),
    disposition            TEXT NOT NULL CHECK (disposition IN ('reopened','exhausted')),
    reason                 TEXT NOT NULL,
    created_at             TEXT NOT NULL,
    FOREIGN KEY (session_id, resource_id)
        REFERENCES session_resources(session_id, resource_id),
    UNIQUE (validation_run_id, resource_id),
    UNIQUE (
        session_id, resource_id, definition_hash, source_kind, source_hash,
        retry_number
    )
);

CREATE INDEX idx_behavioral_witness_repair_epoch
    ON behavioral_witness_repair_cycles(
        session_id, resource_id, definition_hash, source_kind, source_hash,
        retry_number
    );

CREATE INDEX idx_behavioral_witness_repair_exhaustion
    ON behavioral_witness_repair_cycles(
        session_id, resource_id, disposition, created_at
    );
