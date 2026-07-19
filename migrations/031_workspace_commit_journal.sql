-- A candidate commit crosses two durability domains: SQLite governance state
-- and the project filesystem. Record the exact pre-commit workspace bytes
-- before the first file write so process death cannot leave a partially
-- installed candidate masquerading as either the old or accepted tree.

CREATE TABLE workspace_commit_journals (
    id             TEXT PRIMARY KEY,
    execution_id   TEXT NOT NULL UNIQUE REFERENCES execution_manifests(id),
    candidate_id   TEXT UNIQUE REFERENCES candidate_sets(id),
    project_root   TEXT NOT NULL,
    status         TEXT NOT NULL CHECK (status IN (
                       'prepared','workspace_applied','committed','rolled_back'
                   )),
    resolution     TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    resolved_at    TEXT
);
CREATE INDEX idx_workspace_commit_journals_pending
    ON workspace_commit_journals(status, created_at, id);

CREATE TABLE workspace_commit_journal_files (
    journal_id             TEXT NOT NULL REFERENCES workspace_commit_journals(id) ON DELETE CASCADE,
    path                   TEXT NOT NULL,
    original_exists        INTEGER NOT NULL CHECK (original_exists IN (0,1)),
    original_content_blob  TEXT REFERENCES content_blobs(hash),
    original_content_hash  TEXT NOT NULL DEFAULT '',
    original_mode          INTEGER NOT NULL DEFAULT 0 CHECK (original_mode >= 0),
    candidate_content_blob TEXT NOT NULL REFERENCES content_blobs(hash),
    candidate_content_hash TEXT NOT NULL,
    candidate_mode         INTEGER NOT NULL CHECK (candidate_mode >= 0),
    write_intent           TEXT NOT NULL CHECK (write_intent IN ('create','modify','preserve')),
    ordinal                INTEGER NOT NULL CHECK (ordinal >= 0),
    PRIMARY KEY (journal_id, path),
    UNIQUE (journal_id, ordinal),
    CHECK (
        (original_exists = 1 AND original_content_blob IS NOT NULL AND original_content_hash != '')
        OR
        (original_exists = 0 AND original_content_blob IS NULL AND original_content_hash = '' AND original_mode = 0)
    )
);
CREATE INDEX idx_workspace_commit_journal_files_journal
    ON workspace_commit_journal_files(journal_id, ordinal);
