-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Auth foundation. Adds the tables that auth+authz needs: orgs (the
-- tenant boundary every other table has been org_id-scoped against),
-- users (mirrored from Keycloak by sub), org_members (the (user, org,
-- role) join), service_accounts, and api_tokens (covering both
-- personal access tokens and service-account tokens via a single
-- hashed-value table with an owner discriminator).
--
-- This migration is PURELY ADDITIVE — no existing table is altered.
-- It seeds:
--   - the "Default" org, using the UUID that the rest of the code
--     already treats as `integrations.DefaultOrgID`, so all existing
--     rows continue to belong to a real org.
--   - the admin user mirror, with the same UUID as the user seeded
--     into Keycloak's realm-export.json. The mapping
--     users.keycloak_sub ↔ keycloak users.id is what lets the
--     middleware resolve a JWT to a Conduit user.
--
-- The middleware that USES these tables lands in P2; this migration
-- can land safely without breaking the current cell-api (which
-- continues to use the hard-coded DefaultOrgID).

-- ── orgs ───────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS orgs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Human-friendly slug used in URLs once the org switcher lands
    -- (e.g. /o/<slug>/services). Unique globally; immutable after
    -- create (we'd add a redirect table if rename becomes a thing).
    slug        TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_orgs_slug ON orgs(slug);

-- ── users ──────────────────────────────────────────────────────────────
-- Mirror of the user identity that Keycloak owns. We keep just enough
-- to render names + emails without round-tripping Keycloak on every
-- request, and to have a stable FK target for org_members /
-- api_tokens. Updated lazily on each login from the JWT claims.
CREATE TABLE IF NOT EXISTS users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Keycloak's user UUID — the `sub` claim on every issued JWT.
    -- Unique because two Conduit users can't share a Keycloak
    -- identity. Indexed because every authenticated request looks
    -- this up.
    keycloak_sub    UUID NOT NULL UNIQUE,
    email           TEXT NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    -- last_login_at lets the audit / inactivity tooling find dormant
    -- accounts. Null = never signed in (only the seed user before
    -- first login).
    last_login_at   TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_users_keycloak_sub ON users(keycloak_sub);
CREATE INDEX IF NOT EXISTS idx_users_email ON users(lower(email));

-- ── org_members ────────────────────────────────────────────────────────
-- The (user, org) → role relationship. A user can belong to many orgs
-- with potentially different roles in each. Role is a closed enum
-- enforced at the column level so a typo in app code can't grant a
-- non-existent role.
CREATE TABLE IF NOT EXISTS org_members (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    role        TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, org_id)
);
CREATE INDEX IF NOT EXISTS idx_org_members_org ON org_members(org_id);

-- ── service_accounts ───────────────────────────────────────────────────
-- Non-human principals owned by an org. Each service account has its
-- own role inside the org (same closed enum as org_members) and can
-- own one or more api_tokens. Used for automation (CI/CD, the
-- autonomous worker if we route it through the API, integrations
-- with downstream systems).
CREATE TABLE IF NOT EXISTS service_accounts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    role        TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Name is unique per org so two automations can't collide.
    UNIQUE (org_id, name)
);
CREATE INDEX IF NOT EXISTS idx_service_accounts_org ON service_accounts(org_id);

-- ── api_tokens ─────────────────────────────────────────────────────────
-- A single table covers both personal access tokens (owner_type='user'
-- — token inherits the user's role per their org_members rows) and
-- service-account tokens (owner_type='service_account' — token uses
-- the service_account's role in its single owning org).
--
-- We store ONLY the hash. The plaintext token is shown to the user
-- exactly once at creation time; a lost token gets revoked + reissued.
-- prefix is the user-visible identifier they see in token lists
-- (e.g. "con_pat_a1b2c3d4..."); we display prefix||"…" plus the
-- hash never leaves the DB.
CREATE TABLE IF NOT EXISTS api_tokens (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_type    TEXT NOT NULL CHECK (owner_type IN ('user', 'service_account')),
    -- Polymorphic owner FK enforced via the two partial constraints
    -- below. We accept the trade-off for query simplicity over the
    -- strict FK polymorphism of two separate join tables.
    owner_id      UUID NOT NULL,
    name          TEXT NOT NULL,
    -- First 12 chars of the encoded token, kept in plaintext so the
    -- UI can render "con_pat_a1b2c3d4…" without recovering the full
    -- token. Not security-sensitive on its own.
    prefix        TEXT NOT NULL,
    -- argon2id hash of the full token. Verified by the middleware on
    -- every authenticated request that uses bearer tokens.
    token_hash    TEXT NOT NULL,
    last_used_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at    TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_owner ON api_tokens(owner_type, owner_id);
CREATE INDEX IF NOT EXISTS idx_api_tokens_prefix ON api_tokens(prefix) WHERE revoked_at IS NULL;

-- Polymorphic-FK enforcement: ensure owner_id points at an actual
-- row in the right table for its owner_type. Done as ALTER TABLE
-- CHECK constraints would be simpler but Postgres doesn't allow
-- subqueries in CHECKs; trigger functions are heavier. For now the
-- application layer is responsible — a stale orphan token would
-- merely fail to authenticate (the lookup join returns no role).

-- ── seed: Default org + admin user + admin membership ──────────────────
-- The "Default" org uses the same UUID the cell-api hard-codes as
-- integrations.DefaultOrgID, so every existing row keeps a valid
-- parent. Once auth middleware lands in P2, the hard-coded constant
-- gets removed entirely and orgs are always derived from the
-- authenticated principal.
INSERT INTO orgs (id, slug, name)
VALUES ('00000000-0000-0000-0000-000000000001', 'default', 'Default')
ON CONFLICT (id) DO NOTHING;

-- Admin user mirror — the UUID matches the user seeded into
-- realm-export.json so its Keycloak `sub` lines up.
INSERT INTO users (id, keycloak_sub, email, name)
VALUES (
    '00000000-0000-0000-0000-000000000a11',
    '00000000-0000-0000-0000-000000000a11',
    'admin@conduit.local',
    'Admin User'
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO org_members (user_id, org_id, role)
VALUES (
    '00000000-0000-0000-0000-000000000a11',
    '00000000-0000-0000-0000-000000000001',
    'admin'
)
ON CONFLICT (user_id, org_id) DO NOTHING;
