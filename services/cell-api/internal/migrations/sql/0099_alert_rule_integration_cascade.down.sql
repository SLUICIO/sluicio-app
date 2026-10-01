-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
DROP INDEX IF EXISTS alert_rules_integration_idx;
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_integration_id_fkey;
ALTER TABLE alert_rules
    ADD CONSTRAINT alert_rules_integration_id_fkey
    FOREIGN KEY (integration_id) REFERENCES integrations(id) ON DELETE SET NULL;
