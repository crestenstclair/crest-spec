-- Shared module files (src/lib.rs, mod.rs) are committed by many generators
-- over time. With path as the sole primary key, ownership was
-- last-committer-wins, so destroying whichever resource happened to commit a
-- shared file last deleted it from disk (bit us: destroying the Plugin entity
-- deleted src/lib.rs). Ownership is now (path, resource_id): every committer
-- holds a row, and destroy only removes the file when the last owner leaves.
CREATE TABLE generated_files_new (
    path         TEXT NOT NULL,
    resource_id  TEXT NOT NULL REFERENCES resources(id) ON DELETE CASCADE,
    content_hash TEXT NOT NULL,
    prompt_hash  TEXT NOT NULL,
    model        TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    PRIMARY KEY (path, resource_id)
);

INSERT INTO generated_files_new (path, resource_id, content_hash, prompt_hash, model, created_at)
SELECT path, resource_id, content_hash, prompt_hash, model, created_at FROM generated_files;

DROP TABLE generated_files;
ALTER TABLE generated_files_new RENAME TO generated_files;

CREATE INDEX idx_generated_files_resource ON generated_files(resource_id);
CREATE INDEX idx_generated_files_path ON generated_files(path);
