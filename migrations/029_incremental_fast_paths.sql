-- Persist the direct dependency contract that an accepted implementation was
-- generated for or subsequently proved compatible with. Empty is an explicit
-- legacy/unknown marker and is never eligible for candidate reuse.
ALTER TABLE resources
    ADD COLUMN dependency_contract_hash TEXT NOT NULL DEFAULT '';

-- Candidate input identity is immutable acceptance provenance. Reuse queries
-- match these columns directly; the mutable resources projection is never
-- treated as proof of what governed an older candidate.
ALTER TABLE candidate_sets
    ADD COLUMN generation_input_hash TEXT NOT NULL DEFAULT '';

ALTER TABLE candidate_sets
    ADD COLUMN dependency_contract_hash TEXT NOT NULL DEFAULT '';

-- The host never dispatches validate_existing/reuse_candidate resources to a
-- model. Keeping the strategy on the session resource makes the shortcut
-- durable and lets scheduling distinguish terminal fast paths from real work.
ALTER TABLE session_resources
    ADD COLUMN execution_strategy TEXT NOT NULL DEFAULT 'generate'
        CHECK (execution_strategy IN ('generate','validate_existing','reuse_candidate'));

ALTER TABLE session_resources
    ADD COLUMN source_candidate_id TEXT REFERENCES candidate_sets(id);

-- Append-only, content-addressed audit trail for cache hits. The referenced
-- candidate remains immutable; these hashes record the exact governed inputs
-- that were rechecked before the hit was accepted into the new session.
CREATE TABLE candidate_reuses (
    id                       TEXT PRIMARY KEY,
    session_id               TEXT NOT NULL REFERENCES agent_sessions(id),
    resource_id              TEXT NOT NULL,
    source_candidate_id      TEXT NOT NULL REFERENCES candidate_sets(id),
    candidate_hash           TEXT NOT NULL,
    generation_input_hash    TEXT NOT NULL CHECK (generation_input_hash <> ''),
    dependency_contract_hash TEXT NOT NULL CHECK (dependency_contract_hash <> ''),
    reused_at                TEXT NOT NULL,
    UNIQUE (session_id, resource_id)
);

CREATE INDEX idx_candidate_reuses_source
    ON candidate_reuses(source_candidate_id, reused_at);
