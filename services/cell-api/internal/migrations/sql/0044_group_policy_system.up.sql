-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Add a sixth group-access-policy kind: 'system'. Grants a team every
-- service flagged is_system, optionally narrowed to one system_kind
-- (e.g. all RabbitMQ). A specific system stays grantable via kind='service'.
-- target_system_kind: NULL/'' = all systems; a value = only that kind.

ALTER TABLE group_access_policies ADD COLUMN IF NOT EXISTS target_system_kind TEXT;

-- Both CHECKs must allow 'system': the inline kind list and the shape guard.
ALTER TABLE group_access_policies DROP CONSTRAINT IF EXISTS group_access_policies_kind_check;
ALTER TABLE group_access_policies DROP CONSTRAINT IF EXISTS chk_policy_shape;

ALTER TABLE group_access_policies
    ADD CONSTRAINT group_access_policies_kind_check
    CHECK (kind IN ('service', 'integration', 'attributes', 'compound', 'all_org', 'system'));

ALTER TABLE group_access_policies
    ADD CONSTRAINT chk_policy_shape CHECK (
        (kind = 'service'     AND target_service_name IS NOT NULL AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL)
     OR (kind = 'integration' AND target_service_name IS NULL     AND target_integration_id IS NOT NULL AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL)
     OR (kind = 'attributes'  AND target_service_name IS NULL     AND target_integration_id IS NULL     AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb AND target_system_kind IS NULL)
     OR (kind = 'compound'    AND (target_service_name IS NOT NULL OR target_integration_id IS NOT NULL) AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb AND target_system_kind IS NULL)
     OR (kind = 'all_org'     AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL)
     OR (kind = 'system'      AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb)
    );
