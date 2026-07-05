-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Native auth refactor. Migration 0017 designed Conduit to defer
-- identity to Keycloak. We've since decided Conduit should ship a
-- usable login out of the box — local password auth in the cell-api,
-- with OPTIONAL OIDC federation that a customer can configure once
-- they're ready to plug into their existing IdP. See docs/auth.md.
--
-- What changes structurally:
--   1) users.keycloak_sub goes away. The "this user's external
--      identity in some IdP" relationship moves into oidc_subjects,
--      a many-to-one join keyed by (provider_id, external_sub) so a
--      user can have identities in multiple IdPs over time without
--      schema churn.
--   2) users gains password_hash + must_reset_password for the
--      native-login path. Both are nullable — password_hash NULL
--      means "no local password, can only sign in via OIDC."
--   3) New sessions table holds opaque server-side session ids that
--      back the HTTP-only cookie the cell-api sets on login. (We
--      prefer this over self-signed JWTs because it makes revoke
--      trivial — delete the row.)
--   4) New auth_providers table holds the org-scoped OIDC config a
--      customer enters via the "Settings → SSO" page (lands later
--      in P5). Each provider has the issuer URL + client_id +
--      client_secret + how to map claims to user fields.

-- 1) users: drop keycloak_sub, add password_hash + must_reset_password
ALTER TABLE users
    DROP COLUMN IF EXISTS keycloak_sub;
DROP INDEX IF EXISTS idx_users_keycloak_sub;

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS password_hash TEXT,
    ADD COLUMN IF NOT EXISTS must_reset_password BOOLEAN NOT NULL DEFAULT FALSE;

-- 2) oidc_subjects: each OIDC sign-in records the (provider, external
--    `sub`) it came from. A user can have multiple — one per provider
--    they've signed in via — so when an enterprise customer switches
--    IdPs we don't lose the link. Keyed on (provider_id, external_sub)
--    so two providers can both use UUID-shaped subs without colliding.
CREATE TABLE IF NOT EXISTS oidc_subjects (
    provider_id    UUID NOT NULL,
    external_sub   TEXT NOT NULL,
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at   TIMESTAMPTZ,
    PRIMARY KEY (provider_id, external_sub)
);
CREATE INDEX IF NOT EXISTS idx_oidc_subjects_user ON oidc_subjects(user_id);

-- 3) sessions: opaque session ids backed by the HTTP-only cookie the
--    cell-api sets on login. Logging out = deleting the row; expiry
--    is enforced by the middleware against expires_at, with a sliding
--    last_used_at so an active session doesn't time out mid-use.
CREATE TABLE IF NOT EXISTS sessions (
    -- Random 32-byte base64url string. Stored verbatim; the cookie
    -- carries the same value, so lookup is a single indexed PK
    -- read per request. Treating session ids as a bearer secret is
    -- the standard cookie-session pattern.
    id            TEXT PRIMARY KEY,
    user_id       UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    -- User-Agent at session creation, kept for the "active sessions"
    -- list a user might view in their account settings. Not used for
    -- authentication decisions (can be spoofed).
    user_agent    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- 4) auth_providers: per-org OIDC provider configs. NULL until an org
--    admin configures one via the (P5) Settings → SSO surface.
--    client_secret is stored encrypted with the cell-api's data key
--    in production; for the dev DB it can be plaintext (the table
--    column doesn't care). The application layer enforces the
--    encrypt-on-write rule.
CREATE TABLE IF NOT EXISTS auth_providers (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    -- Display name shown on the login page button ("Sign in with
    -- ${name}"). Up to the admin who configured it.
    name                TEXT NOT NULL,
    -- "oidc" is the only kind for now. SAML lands later via the
    -- same table with kind='saml' + a different set of columns
    -- behind a kind discriminator.
    kind                TEXT NOT NULL CHECK (kind IN ('oidc')),
    -- Standard OIDC client knobs. We fetch the JWKS / endpoints
    -- from `${issuer_url}/.well-known/openid-configuration`.
    issuer_url          TEXT NOT NULL,
    client_id           TEXT NOT NULL,
    client_secret       TEXT NOT NULL DEFAULT '',
    -- Claim names that map to Conduit user fields. Defaults align
    -- with the OIDC standard claims; admins can override when their
    -- IdP uses non-standard names.
    claim_email         TEXT NOT NULL DEFAULT 'email',
    claim_name          TEXT NOT NULL DEFAULT 'name',
    claim_sub           TEXT NOT NULL DEFAULT 'sub',
    -- enabled=false hides the provider from the login page without
    -- deleting its config; useful when rotating credentials.
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, name)
);
CREATE INDEX IF NOT EXISTS idx_auth_providers_org ON auth_providers(org_id);
