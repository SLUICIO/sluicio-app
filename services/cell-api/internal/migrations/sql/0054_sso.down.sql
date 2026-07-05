DROP TABLE IF EXISTS sso_login_states;
DROP TABLE IF EXISTS auth_provider_claim_mappings;

DO $$ BEGIN
    ALTER TABLE auth_providers DROP CONSTRAINT IF EXISTS auth_providers_default_role_chk;
EXCEPTION WHEN undefined_object THEN NULL; END $$;

ALTER TABLE auth_providers
    DROP COLUMN IF EXISTS jit_provisioning,
    DROP COLUMN IF EXISTS default_role,
    DROP COLUMN IF EXISTS claim_groups,
    DROP COLUMN IF EXISTS scopes;
