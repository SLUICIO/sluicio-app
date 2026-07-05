-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- Bind an alert rule to a service (in addition to an integration), so a
-- metric formula can define a single service's healthy state. A rule may
-- target a service, an integration, both, or neither (a plain alert).

ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS service_name TEXT;

CREATE INDEX IF NOT EXISTS alert_rules_service_idx
    ON alert_rules (organization_id, service_name)
    WHERE service_name IS NOT NULL;
