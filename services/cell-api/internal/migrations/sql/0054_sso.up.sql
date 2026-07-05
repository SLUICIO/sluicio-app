-- SSO/OIDC (EE): extend provider config, add claim → role/team mappings, and a
-- transient login-state table for PKCE/CSRF. See docs/sso.md.

ALTER TABLE auth_providers
    ADD COLUMN IF NOT EXISTS scopes           TEXT    NOT NULL DEFAULT 'openid email profile',
    ADD COLUMN IF NOT EXISTS claim_groups     TEXT    NOT NULL DEFAULT 'groups',
    ADD COLUMN IF NOT EXISTS default_role     TEXT    NOT NULL DEFAULT 'viewer',
    ADD COLUMN IF NOT EXISTS jit_provisioning BOOLEAN NOT NULL DEFAULT TRUE;

-- default_role is constrained to the role enum. Added separately so the
-- migration is re-runnable even if the column already exists.
DO $$ BEGIN
    ALTER TABLE auth_providers
        ADD CONSTRAINT auth_providers_default_role_chk
        CHECK (default_role IN ('admin','editor','viewer'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- Each row maps one IdP groups-claim value to an org role and/or a team
-- (groups) membership. At least one target must be set.
CREATE TABLE IF NOT EXISTS auth_provider_claim_mappings (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id UUID NOT NULL REFERENCES auth_providers(id) ON DELETE CASCADE,
    claim_value TEXT NOT NULL,
    org_role    TEXT CHECK (org_role IN ('admin','editor','viewer')),
    group_id    UUID REFERENCES groups(id) ON DELETE CASCADE,
    group_role  TEXT NOT NULL DEFAULT 'viewer' CHECK (group_role IN ('admin','editor','viewer')),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (org_role IS NOT NULL OR group_id IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS idx_claim_mappings_provider ON auth_provider_claim_mappings(provider_id);

-- Transient per-login state: PKCE verifier + nonce + CSRF state, single-use,
-- short TTL. Postgres-backed so it survives multiple cell-api replicas.
CREATE TABLE IF NOT EXISTS sso_login_states (
    state         TEXT PRIMARY KEY,
    provider_id   UUID NOT NULL REFERENCES auth_providers(id) ON DELETE CASCADE,
    nonce         TEXT NOT NULL,
    code_verifier TEXT NOT NULL,
    redirect_to   TEXT NOT NULL DEFAULT '/',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sso_login_states_expires ON sso_login_states(expires_at);
