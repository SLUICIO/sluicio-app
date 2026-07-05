-- Query-backed custom metrics. Until now a custom metric's value was
-- always pushed in externally (source='pushed', value lives in
-- service_custom_metric_values). A 'query' metric instead binds to an
-- OTLP metric series: Sluicio computes the value itself by aggregating
-- `metric_name` over `window_seconds`, filtered by `attrs`
-- (attribute = value, AND-combined). The existing threshold still
-- drives health. Existing rows default to 'pushed' — no behaviour change.
ALTER TABLE service_custom_metrics
    ADD COLUMN IF NOT EXISTS source         TEXT    NOT NULL DEFAULT 'pushed',
    ADD COLUMN IF NOT EXISTS metric_name    TEXT,
    ADD COLUMN IF NOT EXISTS aggregation    TEXT,
    ADD COLUMN IF NOT EXISTS attrs          JSONB   NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS window_seconds INTEGER NOT NULL DEFAULT 300;
