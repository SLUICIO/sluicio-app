-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- Bind a health check to a SYSTEM (issue #13).
--
-- A check could already govern a service or an integration. A system was
-- the only first-class entity left out, which matters most for the
-- built-in system types: "the Kafka cluster is healthy iff consumer lag
-- < X" describes the cluster, not any one broker, and there was no way
-- to say it.
--
-- Deliberately NOT adding a CHECK that at most one scope is set. The
-- schema has been permissive since 0009 ("a rule may target a service,
-- an integration, both, or neither"), rows exist under that contract,
-- and a constraint added now could fail on live data during migration.
-- Precedence is resolved in code instead (system → integration →
-- service), and the API rejects ambiguous NEW rules.
--
-- ON DELETE CASCADE, unlike integration_id's SET NULL: a check that
-- exists to describe one system is meaningless once that system is gone,
-- and silently converting it into an org-wide rule that still fires and
-- still notifies is worse than removing it. Integrations kept SET NULL
-- for backwards compatibility; new scopes need not inherit that.

ALTER TABLE alert_rules
    ADD COLUMN IF NOT EXISTS system_id UUID REFERENCES systems(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS alert_rules_system_idx
    ON alert_rules (organization_id, system_id)
    WHERE system_id IS NOT NULL;
