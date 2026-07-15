-- Link the legacy generation history projection to the canonical Phase 5
-- execution manifest without duplicating context or candidate content.
ALTER TABLE generations
ADD COLUMN execution_id TEXT REFERENCES execution_manifests(id);

CREATE UNIQUE INDEX idx_generations_execution
ON generations(execution_id) WHERE execution_id IS NOT NULL;
