-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- RBAC v2 phase 1 (docs/rbac-v2-design.md §4): system-INSTANCE grants.
-- kind='system' policies could target all systems or one system_kind;
-- integrations have always been grantable per instance (by UUID). Systems
-- are first-class peers, so they get the same: target_system_id narrows a
-- system policy to one system entity. Backs the CE "attach a group to
-- this system as viewer" surface. At most one narrowing applies:
-- target_system_kind OR target_system_id OR neither (= all systems).

ALTER TABLE group_access_policies
    ADD COLUMN IF NOT EXISTS target_system_id UUID REFERENCES systems(id) ON DELETE CASCADE;

ALTER TABLE group_access_policies DROP CONSTRAINT IF EXISTS chk_policy_shape;
ALTER TABLE group_access_policies
    ADD CONSTRAINT chk_policy_shape CHECK (
        (kind = 'service'     AND target_service_name IS NOT NULL AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL AND conditions IS NULL)
     OR (kind = 'integration' AND target_service_name IS NULL     AND target_integration_id IS NOT NULL AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL AND conditions IS NULL)
     OR (kind = 'attributes'  AND target_service_name IS NULL     AND target_integration_id IS NULL     AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL AND conditions IS NULL)
     OR (kind = 'compound'    AND (target_service_name IS NOT NULL OR target_integration_id IS NOT NULL) AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL AND conditions IS NULL)
     OR (kind = 'all_org'     AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL AND conditions IS NULL)
     OR (kind = 'system'      AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND conditions IS NULL
                              AND NOT (target_system_kind IS NOT NULL AND target_system_id IS NOT NULL))
     OR (kind = 'expression'  AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL AND conditions IS NOT NULL AND jsonb_typeof(conditions) = 'object')
    );

CREATE INDEX IF NOT EXISTS idx_policies_system ON group_access_policies(target_system_id) WHERE target_system_id IS NOT NULL;
