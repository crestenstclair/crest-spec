-- Resource-scoped spec amendments. Materialized cache over the CUE source of
-- truth: `state` and `applied_spec_hash` are recomputed from spec-vs-committed
-- hash during plan/begin reconciliation. Never an independent authority.
CREATE TABLE IF NOT EXISTS amendments (
    id                TEXT PRIMARY KEY,
    resource_id       TEXT NOT NULL,
    name              TEXT NOT NULL,
    content_hash      TEXT NOT NULL,
    origin            TEXT NOT NULL DEFAULT 'manual',
    prompt            TEXT NOT NULL DEFAULT '',
    finding_json      TEXT NOT NULL DEFAULT '',
    validation_json   TEXT NOT NULL DEFAULT '',
    state             TEXT NOT NULL DEFAULT 'PENDING'
                      CHECK (state IN ('PENDING','APPLIED','VERIFIED','GRADUATED','FAILED')),
    applied_spec_hash TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    applied_at        TEXT NOT NULL DEFAULT '',
    graduated_at      TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX idx_amendments_resource_name ON amendments(resource_id, name);
CREATE INDEX idx_amendments_state ON amendments(state);
