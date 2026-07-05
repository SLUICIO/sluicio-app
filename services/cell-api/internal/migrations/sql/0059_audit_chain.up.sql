-- Tamper-evident audit log: each entry stores its own content hash and the
-- previous entry's hash (per-org chain), so deleting or editing a row breaks
-- the chain detectably. Rows written before this migration keep empty hashes
-- and are reported as an unverifiable legacy prefix by the verifier.
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS entry_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS prev_hash  TEXT NOT NULL DEFAULT '';

-- Retention pruning deletes the oldest entries; the anchor remembers the
-- last pruned row's (id, hash) per org so the remaining chain still has a
-- verifiable seed instead of a dangling prev_hash.
CREATE TABLE IF NOT EXISTS audit_chain_anchor (
    organization_id UUID PRIMARY KEY,
    last_id         BIGINT NOT NULL,
    last_hash       TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
