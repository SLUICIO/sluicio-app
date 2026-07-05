-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Groups — the second axis of access control.
--
-- Conduit already scopes everything to an `org` (one customer = one
-- tenant). On top of that, customers want to divide their users into
-- teams ("Group A", "Group B") and scope which DATA each team sees +
-- mutates. A user can be in many groups; a resource can be in many
-- groups; visibility is the set-overlap between a user's group
-- memberships and a resource's group memberships.
--
-- This migration introduces:
--   - groups            — named subsets within an org
--   - group_members     — (user, group) → role (admin|editor|viewer)
--   - service_groups    — which services live in which groups
--
-- Visibility rule (enforced in handlers, not the DB):
--   - Org admins see everything regardless of group membership
--     (they're the management role; they oversee the org).
--   - Non-admins see only resources where at least one of the
--     resource's group_ids is in their group memberships.
--   - A user with zero group memberships sees no group-scoped
--     resources — strict isolation by default.
--
-- The role on group_members is independent from the role on
-- org_members. A user can be org-viewer but group-A-admin, in which
-- case they can mutate Group A's resources but can't manage org-
-- level things (members, SSO). Conversely org-admin trumps any
-- per-group role for read access.

-- ── groups ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS groups (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    -- slug is the URL-safe identifier shown in member-facing flows
    -- (e.g. /groups/orders). Unique within an org; immutable after
    -- create (a rename surface would need a redirect table).
    slug        TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX IF NOT EXISTS idx_groups_org ON groups(org_id);

-- ── group_members ──────────────────────────────────────────────────────
-- Per-(user, group) role. Independent from org_members.role. A user
-- can be in many groups with different roles in each.
CREATE TABLE IF NOT EXISTS group_members (
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id    UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    role        TEXT NOT NULL CHECK (role IN ('admin', 'editor', 'viewer')),
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, group_id)
);
CREATE INDEX IF NOT EXISTS idx_group_members_group ON group_members(group_id);

-- ── service_groups ─────────────────────────────────────────────────────
-- Many-to-many service-to-group association. service_name is text
-- (not a FK to services.service_name because the services catalog is
-- discovered from telemetry — services may be referenced here before
-- they exist in the catalog, and rows here should survive a catalog
-- delete). org_id is included so a single index can support the per-
-- org filter the visibility check uses.
CREATE TABLE IF NOT EXISTS service_groups (
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    group_id     UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    assigned_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, service_name, group_id)
);
CREATE INDEX IF NOT EXISTS idx_service_groups_by_group ON service_groups(group_id);
CREATE INDEX IF NOT EXISTS idx_service_groups_by_service ON service_groups(org_id, service_name);
