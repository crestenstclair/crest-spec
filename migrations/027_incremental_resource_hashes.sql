-- Split direct generation inputs from dependency-facing contracts so planning
-- can reconcile only the dirty dependency frontier. Empty values are a legacy
-- baseline marker and are adopted without forcing a full-project regeneration.
ALTER TABLE resources
    ADD COLUMN generation_input_hash TEXT NOT NULL DEFAULT '';

ALTER TABLE resources
    ADD COLUMN exported_contract_hash TEXT NOT NULL DEFAULT '';
