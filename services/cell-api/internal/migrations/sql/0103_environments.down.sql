-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
ALTER TABLE notification_profiles DROP COLUMN IF EXISTS environments, DROP COLUMN IF EXISTS environment_mode;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS environments, DROP COLUMN IF EXISTS environment_mode;
ALTER TABLE integrations DROP COLUMN IF EXISTS declared_environments;
