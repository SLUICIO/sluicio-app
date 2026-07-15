-- Reverse service-account scoping: drop SA memberships, restore the
-- user-only shape and the original composite PK.
DELETE FROM group_members WHERE service_account_id IS NOT NULL;
DROP INDEX IF EXISTS idx_group_members_sa;
ALTER TABLE group_members DROP CONSTRAINT IF EXISTS group_members_sa_group_key;
ALTER TABLE group_members DROP CONSTRAINT IF EXISTS group_members_user_group_key;
ALTER TABLE group_members DROP CONSTRAINT IF EXISTS group_members_one_principal;
ALTER TABLE group_members DROP COLUMN IF EXISTS service_account_id;
ALTER TABLE group_members ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE group_members ADD PRIMARY KEY (user_id, group_id);

ALTER TABLE service_accounts DROP COLUMN IF EXISTS scope;
