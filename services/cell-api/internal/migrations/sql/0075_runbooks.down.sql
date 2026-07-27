-- SPDX-License-Identifier: FSL-1.1-Apache-2.0

ALTER TABLE alert_rules DROP COLUMN IF EXISTS runbook;
ALTER TABLE system_types DROP COLUMN IF EXISTS runbook;
