-- Reverse the Enterprise audit adoption. Leaves the base audit_log table
-- (owned by 0001_initial) intact.
DROP INDEX IF EXISTS audit_log_org_id_idx;
ALTER TABLE audit_log DROP COLUMN IF EXISTS ip;
ALTER TABLE audit_log DROP COLUMN IF EXISTS actor_email;
ALTER TABLE audit_log DROP COLUMN IF EXISTS actor_name;
