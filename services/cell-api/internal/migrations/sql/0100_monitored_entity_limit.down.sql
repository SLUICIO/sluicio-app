-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
DROP TRIGGER IF EXISTS integrations_entity_limit ON integrations;
DROP TRIGGER IF EXISTS systems_entity_limit ON systems;
DROP FUNCTION IF EXISTS enforce_monitored_entity_limit();
DROP TABLE IF EXISTS deployment_limits;
