-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Systems as first-class entities (docs/systems.md phase 2). A system is an
-- instance of a system type (type_key resolves against the system-types
-- catalog) that spans one or more member services. Membership is one-to-many:
-- services.system_id points at the owning system. A service's is_system /
-- system_kind stay populated (kept in sync with membership) so existing
-- health, badges, templates, and RBAC keep working unchanged.

CREATE TABLE IF NOT EXISTS systems (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL,
    name        TEXT NOT NULL,
    type_key    TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS systems_org_name_idx ON systems (org_id, name);

ALTER TABLE services
    ADD COLUMN IF NOT EXISTS system_id UUID REFERENCES systems(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS services_system_id_idx ON services (system_id);

-- Migrate existing flagged services into systems, grouped by (org, kind): one
-- system per kind, named after the kind ('system' for the generic flag). Users
-- can rename, split, or create additional systems afterwards.
INSERT INTO systems (org_id, name, type_key)
SELECT DISTINCT organization_id, COALESCE(NULLIF(system_kind, ''), 'system'), COALESCE(system_kind, '')
FROM services
WHERE is_system = true
ON CONFLICT (org_id, name) DO NOTHING;

UPDATE services s
SET system_id = sys.id
FROM systems sys
WHERE sys.org_id = s.organization_id
  AND sys.type_key = COALESCE(s.system_kind, '')
  AND s.is_system = true
  AND s.system_id IS NULL;
