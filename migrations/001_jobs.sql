CREATE TABLE jobs (
    id         TEXT PRIMARY KEY,
    tool       TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'running'
               CHECK (status IN ('running','completed','failed','cancelled','deleted')),
    result     TEXT,
    error      TEXT,
    pid        INTEGER NOT NULL,
    started_at TEXT NOT NULL,
    done_at    TEXT
);
CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_pid ON jobs(pid);
