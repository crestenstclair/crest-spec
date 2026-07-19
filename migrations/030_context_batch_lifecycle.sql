-- One durable interval for the shared planning/registry preparation performed
-- by spec/context_batch. Per-resource context lifecycles begin only after this
-- shared step, so useful effort and concurrency are not multiplied by wave
-- width.
CREATE TABLE context_batches (
    id           TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL REFERENCES agent_sessions(id),
    wave_index   INTEGER NOT NULL CHECK (wave_index >= 0),
    status       TEXT NOT NULL CHECK (status IN ('running','completed','failed')),
    started_at   TEXT NOT NULL,
    completed_at TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);

CREATE INDEX idx_context_batches_session
    ON context_batches(session_id, wave_index, created_at, id);

-- Keep upgrades safe for databases that may have applied the lifecycle table
-- before worker-pool enforcement was introduced.
CREATE INDEX IF NOT EXISTS idx_dispatch_lifecycles_worker
    ON dispatch_lifecycles(host_session_id, worker_id);
