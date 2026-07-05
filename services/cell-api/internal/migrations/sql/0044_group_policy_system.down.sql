-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Reverse 0044: drop kind='system' policies, restore the 5-kind shape.

DELETE FROM group_access_policies WHERE kind = 'system';

ALTER TABLE group_access_policies DROP CONSTRAINT IF EXISTS group_access_policies_kind_check;
ALTER TABLE group_access_policies DROP CONSTRAINT IF EXISTS chk_policy_shape;

ALTER TABLE group_access_policies
    ADD CONSTRAINT group_access_policies_kind_check
    CHECK (kind IN ('service', 'integration', 'attributes', 'compound', 'all_org'));

ALTER TABLE group_access_policies
    ADD CONSTRAINT chk_policy_shape CHECK (
        (kind = 'service'     AND target_service_name IS NOT NULL AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb)
     OR (kind = 'integration' AND target_service_name IS NULL     AND target_integration_id IS NOT NULL AND attribute_match = '{}'::jsonb)
     OR (kind = 'attributes'  AND target_service_name IS NULL     AND target_integration_id IS NULL     AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb)
     OR (kind = 'compound'    AND (target_service_name IS NOT NULL OR target_integration_id IS NOT NULL) AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb)
     OR (kind = 'all_org'     AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb)
    );

ALTER TABLE group_access_policies DROP COLUMN IF EXISTS target_system_kind;
