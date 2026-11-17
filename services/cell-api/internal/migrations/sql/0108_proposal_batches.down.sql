DROP INDEX IF EXISTS proposals_batch;
ALTER TABLE proposals DROP COLUMN IF EXISTS batch_index;
ALTER TABLE proposals DROP COLUMN IF EXISTS batch_id;
