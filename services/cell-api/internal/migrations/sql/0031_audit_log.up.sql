-- The audit_log table already exists (created empty in 0001_initial and so
-- far unused). The Enterprise audit feature adopts it and adds a few columns
-- it needs: a denormalised actor name/email (so listings don't join users)
-- and the client IP. Existing columns are reused as-is:
--   action, resource_type, resource_id, payload (JSONB), occurred_at.
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS actor_name  TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS actor_email TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN IF NOT EXISTS ip          TEXT NOT NULL DEFAULT '';

-- Keyset pagination is by id; the 0001 index is (organization_id, occurred_at).
CREATE INDEX IF NOT EXISTS audit_log_org_id_idx ON audit_log (organization_id, id DESC);
