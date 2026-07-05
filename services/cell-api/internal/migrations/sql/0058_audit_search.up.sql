-- Audit-log search (actor / action / time-range filters on the admin
-- Settings tab). The 0001 (org, occurred_at) and 0031 (org, id) indexes
-- cover time-bounded and paged listings; add the two filters they don't:
-- exact actor lookups and action prefix matches. Actor-text search
-- (ILIKE on name/email) stays a scan within the org partition — audit
-- volumes are admin-action-sized, not telemetry-sized.
CREATE INDEX IF NOT EXISTS audit_log_org_actor_idx
    ON audit_log (organization_id, actor_user_id, id DESC);
CREATE INDEX IF NOT EXISTS audit_log_org_action_idx
    ON audit_log (organization_id, action text_pattern_ops, id DESC);
