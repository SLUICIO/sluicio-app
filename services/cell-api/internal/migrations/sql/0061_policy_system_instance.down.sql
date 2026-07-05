-- Restore the 0060 shape guard (instance-targeted system policies must go
-- first — they'd violate the restored constraint).
DELETE FROM group_access_policies WHERE kind = 'system' AND target_system_id IS NOT NULL;

ALTER TABLE group_access_policies DROP CONSTRAINT IF EXISTS chk_policy_shape;
ALTER TABLE group_access_policies
    ADD CONSTRAINT chk_policy_shape CHECK (
        (kind = 'service'     AND target_service_name IS NOT NULL AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND conditions IS NULL)
     OR (kind = 'integration' AND target_service_name IS NULL     AND target_integration_id IS NOT NULL AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND conditions IS NULL)
     OR (kind = 'attributes'  AND target_service_name IS NULL     AND target_integration_id IS NULL     AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb AND target_system_kind IS NULL AND conditions IS NULL)
     OR (kind = 'compound'    AND (target_service_name IS NOT NULL OR target_integration_id IS NOT NULL) AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb AND target_system_kind IS NULL AND conditions IS NULL)
     OR (kind = 'all_org'     AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND conditions IS NULL)
     OR (kind = 'system'      AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND conditions IS NULL)
     OR (kind = 'expression'  AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND conditions IS NOT NULL AND jsonb_typeof(conditions) = 'object')
    );

DROP INDEX IF EXISTS idx_policies_system;
ALTER TABLE group_access_policies DROP COLUMN IF EXISTS target_system_id;
