ALTER TABLE audit_log DROP COLUMN IF EXISTS entry_hash;
ALTER TABLE audit_log DROP COLUMN IF EXISTS prev_hash;
DROP TABLE IF EXISTS audit_chain_anchor;
