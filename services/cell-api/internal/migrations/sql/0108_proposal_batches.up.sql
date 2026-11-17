-- A batch is several proposals an agent filed together, with one
-- rationale, so that 24 near-identical changes (channels on every check
-- of a cell) are one review rather than 24. Each item is still an
-- ordinary proposal, reviewed, drift-checked and applied by its own
-- kind's rules; batch_id groups them and batch_index keeps the order the
-- agent gave, which is the order approval applies them in. NULL for a
-- proposal filed on its own.
ALTER TABLE proposals ADD COLUMN IF NOT EXISTS batch_id UUID;
ALTER TABLE proposals ADD COLUMN IF NOT EXISTS batch_index INT;
CREATE INDEX IF NOT EXISTS proposals_batch ON proposals (org_id, batch_id) WHERE batch_id IS NOT NULL;
