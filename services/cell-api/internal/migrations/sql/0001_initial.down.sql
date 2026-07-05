-- SPDX-License-Identifier: FSL-1.1-Apache-2.0

DROP TABLE IF EXISTS audit_log;
DROP TABLE IF EXISTS notification_jobs;
DROP TYPE  IF EXISTS notification_job_state;
DROP TABLE IF EXISTS alert_instances;
DROP TYPE  IF EXISTS alert_state;
DROP TABLE IF EXISTS alert_rule_routes;
DROP TABLE IF EXISTS notification_channels;
DROP TABLE IF EXISTS alert_rules;
DROP TYPE  IF EXISTS alert_severity;
DROP TYPE  IF EXISTS alert_signal;
DROP TABLE IF EXISTS integration_matchers;
DROP TYPE  IF EXISTS matcher_operator;
DROP TABLE IF EXISTS integrations;
