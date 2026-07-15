-- Service-account scoping (docs/service-account-scoping-design.md):
-- service accounts become first-class principals in the group model.
--
-- 1. service_accounts.scope: 'scoped' (deny-by-default, visibility via
--    group membership — the default) or 'org_wide' (explicit, audited
--    opt-in to the pre-v0.12 org-wide read behaviour). No backfill
--    special-casing: at the time of this migration no installation has
--    any service_accounts rows, so everything starts scoped.
ALTER TABLE service_accounts
    ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'scoped'
        CHECK (scope IN ('scoped', 'org_wide'));

-- 2. group_members becomes polymorphic: a row is EITHER a user
--    membership OR a service-account membership. The old PK
--    (user_id, group_id) can't survive a nullable user_id, so it is
--    replaced by two plain UNIQUE constraints. Plain (not partial) on
--    purpose: sso.go upserts with ON CONFLICT (user_id, group_id),
--    which needs a full unique index; NULLs are distinct so SA rows
--    never trip the user constraint and vice versa.
ALTER TABLE group_members DROP CONSTRAINT group_members_pkey;
ALTER TABLE group_members ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE group_members
    ADD COLUMN IF NOT EXISTS service_account_id UUID REFERENCES service_accounts(id) ON DELETE CASCADE;
ALTER TABLE group_members
    ADD CONSTRAINT group_members_one_principal
        CHECK ((user_id IS NULL) <> (service_account_id IS NULL));
ALTER TABLE group_members
    ADD CONSTRAINT group_members_user_group_key UNIQUE (user_id, group_id);
ALTER TABLE group_members
    ADD CONSTRAINT group_members_sa_group_key UNIQUE (service_account_id, group_id);
CREATE INDEX IF NOT EXISTS idx_group_members_sa ON group_members(service_account_id)
    WHERE service_account_id IS NOT NULL;
