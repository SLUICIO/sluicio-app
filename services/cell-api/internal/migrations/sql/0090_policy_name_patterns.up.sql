-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Name patterns for integration and system access policies.
--
-- kind='integration' could name exactly one integration by id, and
-- kind='system' one kind or one instance. Neither could say "every
-- integration whose name starts with abc and ends with -at", which is
-- how estates are actually organised: the naming convention IS the
-- grouping, and enumerating today's matches by hand leaves tomorrow's
-- out.
--
-- The pattern reuses the `conditions` tree that kind='expression'
-- already stores, evaluated against a universe of NAMES rather than
-- service names. So this migration adds no column: it relaxes the shape
-- constraint to let those two kinds carry a conditions tree.
--
-- For integration the two are exclusive - one id OR a pattern, never
-- both. A row that named one integration and also described a set would
-- have two answers to "what does this grant" and no way to tell which
-- was meant.
--
-- For system the pattern narrows ALONGSIDE the existing kind filter, so
-- "systems of kind kafka, named prod-*" is one policy. target_system_id
-- stays mutually exclusive with a pattern for the same reason as above.

ALTER TABLE group_access_policies DROP CONSTRAINT IF EXISTS chk_policy_shape;
ALTER TABLE group_access_policies
    ADD CONSTRAINT chk_policy_shape CHECK (
        (kind = 'service'     AND target_service_name IS NOT NULL AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL AND conditions IS NULL)
     OR (kind = 'integration' AND target_service_name IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL
                              AND ((target_integration_id IS NOT NULL AND conditions IS NULL)
                                OR (target_integration_id IS NULL     AND conditions IS NOT NULL AND jsonb_typeof(conditions) = 'object')))
     OR (kind = 'attributes'  AND target_service_name IS NULL     AND target_integration_id IS NULL     AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL AND conditions IS NULL)
     OR (kind = 'compound'    AND (target_service_name IS NOT NULL OR target_integration_id IS NOT NULL) AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL AND conditions IS NULL)
     OR (kind = 'all_org'     AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL AND conditions IS NULL)
     OR (kind = 'system'      AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb
                              AND (conditions IS NULL OR (jsonb_typeof(conditions) = 'object' AND target_system_id IS NULL))
                              AND NOT (target_system_kind IS NOT NULL AND target_system_id IS NOT NULL))
     OR (kind = 'expression'  AND target_service_name IS NULL     AND target_integration_id IS NULL     AND attribute_match = '{}'::jsonb AND target_system_kind IS NULL AND target_system_id IS NULL AND conditions IS NOT NULL AND jsonb_typeof(conditions) = 'object')
    );
