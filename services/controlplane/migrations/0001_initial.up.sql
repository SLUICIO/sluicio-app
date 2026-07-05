-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- Initial control-plane schema: organizations, users, memberships,
-- invitations, and the cell registry. Preliminary.

CREATE TABLE organizations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        TEXT UNIQUE NOT NULL,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    oidc_issuer   TEXT NOT NULL,
    oidc_subject  TEXT NOT NULL,
    email         TEXT NOT NULL,
    display_name  TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (oidc_issuer, oidc_subject)
);
CREATE INDEX users_email_idx ON users (lower(email));

CREATE TYPE membership_role AS ENUM ('owner', 'admin', 'editor', 'viewer');

CREATE TABLE memberships (
    organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role             membership_role NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, user_id)
);

CREATE TABLE invitations (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email            TEXT NOT NULL,
    role             membership_role NOT NULL,
    token            TEXT UNIQUE NOT NULL,
    invited_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    expires_at       TIMESTAMPTZ NOT NULL,
    accepted_at      TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX invitations_email_idx ON invitations (lower(email));

-- Cell registry: the directory of provisioned cells and which tenants
-- live in each.

CREATE TABLE cells (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name             TEXT UNIQUE NOT NULL,
    region           TEXT NOT NULL,
    dedicated        BOOLEAN NOT NULL DEFAULT FALSE,
    api_endpoint     TEXT NOT NULL,
    ingest_endpoint  TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'provisioning',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE tenant_cells (
    organization_id  UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    cell_id          UUID NOT NULL REFERENCES cells(id) ON DELETE RESTRICT,
    PRIMARY KEY (organization_id, cell_id)
);

-- Append-only audit log for control-plane actions.

CREATE TABLE audit_log (
    id                BIGSERIAL PRIMARY KEY,
    organization_id   UUID,
    actor_user_id     UUID,
    action            TEXT NOT NULL,
    resource_type     TEXT NOT NULL,
    resource_id       TEXT,
    payload           JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_org_time_idx ON audit_log (organization_id, occurred_at DESC);
