-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
DROP INDEX IF EXISTS alert_rules_service_idx;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS service_name;
