-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- RBAC v2 phase 3 (docs/rbac-v2-design.md §6): share one integration or
-- system with a user or group. Shares grant VIEW only — there is no role
-- column by design, so manage-via-share can't creep in later. Deleting
-- the resource, user, or group cascades the share. Integrations and
-- systems are peers (resource_kind discriminates; resource_id is not an
-- FK because it points at two tables — handler validates existence and
-- a cleanup trigger isn't needed since both parents cascade via the
-- explicit deletes below... integrations/systems deletes are app-level,
-- so the app deletes shares alongside; grantee FKs cascade in the DB).

CREATE TABLE IF NOT EXISTS resource_shares (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL,
    resource_kind TEXT NOT NULL CHECK (resource_kind IN ('integration','system')),
    resource_id   UUID NOT NULL,
    grantee_kind  TEXT NOT NULL CHECK (grantee_kind IN ('user','group')),
    grantee_id    UUID NOT NULL,
    created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, resource_kind, resource_id, grantee_kind, grantee_id)
);
CREATE INDEX IF NOT EXISTS resource_shares_grantee_idx ON resource_shares (org_id, grantee_kind, grantee_id);
CREATE INDEX IF NOT EXISTS resource_shares_resource_idx ON resource_shares (org_id, resource_kind, resource_id);
