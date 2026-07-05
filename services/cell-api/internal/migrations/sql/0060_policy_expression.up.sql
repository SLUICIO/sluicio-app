-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Seventh group-access-policy kind: 'expression'. Where the other kinds
-- express one fixed shape, this one carries an arbitrary boolean tree
-- (AND / OR / NOT over service-name and resource-attribute leaves) in the
-- `conditions` JSONB column, so a single policy can say things like:
--
--   (service prefix "ABC")
--     AND ((attr "team" = "orders") OR (attr "team" = "payments"))
--     AND NOT (attr "env" = "sandbox")
--
-- Evaluation is a union-of-allows like every other kind — an expression
-- only ever narrows what that one policy grants; it can never revoke
-- access another group granted. The tree is evaluated in Go
-- (identity.evalExpr); the DB just stores it. A malformed/empty tree
-- resolves to the empty set (fail closed).

ALTER TABLE group_access_policies ADD COLUMN IF NOT EXISTS conditions JSONB;

-- Both CHECKs must admit 'expression': the kind whitelist and the shape
-- guard. An expression policy sets `conditions` and nothing else.
ALTER TABLE group_access_policies DROP CONSTRAINT IF EXISTS group_access_policies_kind_check;
ALTER TABLE group_access_policies DROP CONSTRAINT IF EXISTS chk_policy_shape;

ALTER TABLE group_access_policies
    ADD CONSTRAINT group_access_policies_kind_check
    CHECK (kind IN ('service', 'integration', 'attributes', 'compound', 'all_org', 'system', 'expression'));

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
