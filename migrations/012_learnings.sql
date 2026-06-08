-- Craft-level learnings distilled from generation history, injected into prompts.
CREATE TABLE IF NOT EXISTS learnings (
    id                   TEXT PRIMARY KEY,
    scope_lang           TEXT NOT NULL DEFAULT '',
    scope_kind           TEXT NOT NULL DEFAULT '',
    text                 TEXT NOT NULL,
    rationale            TEXT NOT NULL DEFAULT '',
    source_generation_id TEXT,
    source_apply_id      TEXT,
    confidence           REAL NOT NULL DEFAULT 0.5,
    status               TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','retired','promoted')),
    times_applied        INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
);
CREATE INDEX idx_learnings_scope ON learnings(scope_lang, scope_kind, status);
