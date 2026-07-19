-- Preserve the full declaration hash for provenance while separately tracking
-- the declaration subset that can require implementation work. Verification
-- definitions are reconciled by the evidence subsystem.
ALTER TABLE resources
    ADD COLUMN code_declaration_hash TEXT NOT NULL DEFAULT '';

-- Only explicit repair-owner verification contracts are persisted here.
-- Broad observer witnesses remain evidence definitions and never fan out into
-- resource scheduler rows.
ALTER TABLE resources
    ADD COLUMN verification_contract_hash TEXT NOT NULL DEFAULT '';
