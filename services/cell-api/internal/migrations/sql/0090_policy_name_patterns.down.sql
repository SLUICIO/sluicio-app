-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Restores the pre-pattern shape constraint (0061). Any integration or
-- system policy carrying a name pattern is deleted first: the old
-- constraint cannot describe it, so the alternative is a migration that
-- refuses to run on exactly the cells that used the feature.
DELETE FROM group_access_policies
 WHERE kind IN ('integration', 'system') AND conditions IS NOT NULL;

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
