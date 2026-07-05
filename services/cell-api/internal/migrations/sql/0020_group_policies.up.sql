-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Group access policies — the second-axis generalisation of
-- service_groups from 0019. A group still scopes who-sees-what, but
-- *what* can now be expressed as one of:
--
--   service      a specific service_name
--   integration  every service inside one integration
--   attributes   any service / span / log / metric whose resource
--                attributes match all of attribute_match's kv pairs
--   compound     integration OR service-scope PLUS attribute filter
--                (e.g. "Integration A where env=prod AND team=orders")
--   all_org      everything in the org — the wildcard escape hatch
--
-- Effective access for a user = OR across every policy on every
-- group they belong to. Within one row, attribute_match kv pairs
-- AND together.
--
-- 0019's service_groups table is migrated row-for-row into the new
-- table as kind='service' and then dropped — it was live for ~30
-- minutes with no real customer data, so the loss-of-fidelity risk
-- is zero. New code reads from group_access_policies exclusively.

CREATE TABLE IF NOT EXISTS group_access_policies (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    group_id              UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    kind                  TEXT NOT NULL CHECK (kind IN ('service', 'integration', 'attributes', 'compound', 'all_org')),
    -- Populated when kind='service' or kind='compound' with an
    -- explicit service target. NULL otherwise.
    target_service_name   TEXT,
    -- Populated when kind='integration' or kind='compound' with an
    -- explicit integration target. NULL otherwise. The FK is
    -- ON DELETE CASCADE so deleting an integration drops policies
    -- that referenced it.
    target_integration_id UUID REFERENCES integrations(id) ON DELETE CASCADE,
    -- Resource-attribute kv pairs that ALL must match for the policy
    -- to apply (AND semantics). Empty {} when not used; required
    -- non-empty for kind='attributes' (the validator in the API
    -- enforces this). Stored as JSONB so we can index by key for
    -- the catalog-attribute resolver.
    attribute_match       JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Sanity constraints — keep the polymorphic shape honest at the
    -- DB layer instead of trusting the API alone.
    CONSTRAINT chk_policy_shape CHECK (
        (kind = 'service'     AND target_service_name IS NOT NULL AND target_integration_id IS NULL AND attribute_match = '{}'::jsonb)
     OR (kind = 'integration' AND target_service_name IS NULL     AND target_integration_id IS NOT NULL AND attribute_match = '{}'::jsonb)
     OR (kind = 'attributes'  AND target_service_name IS NULL     AND target_integration_id IS NULL AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb)
     OR (kind = 'compound'    AND (target_service_name IS NOT NULL OR target_integration_id IS NOT NULL) AND jsonb_typeof(attribute_match) = 'object' AND attribute_match != '{}'::jsonb)
     OR (kind = 'all_org'     AND target_service_name IS NULL     AND target_integration_id IS NULL AND attribute_match = '{}'::jsonb)
    )
);
CREATE INDEX IF NOT EXISTS idx_policies_group   ON group_access_policies(group_id);
CREATE INDEX IF NOT EXISTS idx_policies_service ON group_access_policies(target_service_name) WHERE target_service_name IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_policies_integ   ON group_access_policies(target_integration_id) WHERE target_integration_id IS NOT NULL;

-- Migrate the 0019 explicit service→group rows into the new model.
INSERT INTO group_access_policies (group_id, kind, target_service_name)
SELECT group_id, 'service', service_name FROM service_groups
ON CONFLICT DO NOTHING;

DROP TABLE IF EXISTS service_groups;
