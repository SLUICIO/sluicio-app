ALTER TABLE service_custom_metrics
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS metric_name,
    DROP COLUMN IF EXISTS aggregation,
    DROP COLUMN IF EXISTS attrs,
    DROP COLUMN IF EXISTS window_seconds;
