DROP INDEX IF EXISTS proposals_pending_dedup_key;
ALTER TABLE proposals DROP COLUMN IF EXISTS dedup_key;
