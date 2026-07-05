-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Unify "custom metrics" into health checks (alert_rules). A health check
-- can now (a) source its value from telemetry — the existing behaviour:
-- aggregate an OTLP metric — or from a value pushed in by an external
-- scraper, and (b) optionally surface its latest reading as a value tile
-- on the service page. The old service_custom_metrics tables are folded
-- into alert_rules and dropped.

-- 1. New columns on alert_rules. Existing rules default to source
--    'telemetry' (they aggregate ClickHouse) and are not displayed.
ALTER TABLE alert_rules
    ADD COLUMN IF NOT EXISTS source             TEXT    NOT NULL DEFAULT 'telemetry',
    ADD COLUMN IF NOT EXISTS display_on_service BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS unit               TEXT;

-- 2. Latest numeric reading per rule. Telemetry rules have it written by
--    the evaluator each tick (when the series has samples); pushed rules
--    by the ingest endpoint. One row per rule (latest only), mirroring
--    the old service_custom_metric_values model — no history in v1.
CREATE TABLE IF NOT EXISTS alert_rule_readings (
    alert_rule_id UUID PRIMARY KEY REFERENCES alert_rules(id) ON DELETE CASCADE,
    value         DOUBLE PRECISION NOT NULL,
    observed_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 3. Migrate existing custom metrics into alert_rules. Each becomes a
--    metric-signal health check bound to its service, displayed on the
--    service page, carrying its unit. 'query' → source 'telemetry';
--    'pushed' → source 'pushed'. rule_spec mirrors MetricRuleSpec; for a
--    pushed metric the metric_name/aggregation are placeholders the
--    pushed evaluator ignores (it compares the pushed value directly).
INSERT INTO alert_rules (
    id, organization_id, service_name, name, description,
    signal, rule_spec, severity, enabled, source, display_on_service, unit,
    created_at, updated_at
)
SELECT
    m.id, m.organization_id, m.service_name, m.name, m.description,
    'metric',
    jsonb_build_object(
        'metric_name', COALESCE(m.metric_name, ''),
        'aggregation', COALESCE(NULLIF(m.aggregation, ''), 'last'),
        'operator',    m.threshold_operator::text,
        'threshold',   m.threshold_value,
        'for_window',  (COALESCE(NULLIF(m.window_seconds, 0), 300)::text || 's'),
        'attrs',       COALESCE(m.attrs, '[]'::jsonb)
    ),
    'warning', TRUE,
    CASE m.source WHEN 'query' THEN 'telemetry' ELSE 'pushed' END,
    TRUE, m.unit,
    m.created_at, m.updated_at
FROM service_custom_metrics m;

-- 4. Carry over the latest pushed/observed values onto the new rules.
INSERT INTO alert_rule_readings (alert_rule_id, value, observed_at)
SELECT v.metric_id, v.value, v.observed_at
FROM service_custom_metric_values v
WHERE EXISTS (SELECT 1 FROM alert_rules r WHERE r.id = v.metric_id);

-- 5. Drop the old tables + enum — fully folded in now.
DROP TABLE IF EXISTS service_custom_metric_values;
DROP TABLE IF EXISTS service_custom_metrics;
DROP TYPE IF EXISTS custom_metric_operator;
