-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- OTLP metrics schema. One row per numeric data point. We flatten the
-- OTLP metric types we care about for thresholding into a single wide
-- table rather than a table-per-type:
--
--   gauge      -> one row per point, Value = the gauge value
--   sum        -> one row per point, Value = the (counter) value,
--                 IsMonotonic = 1 for monotonic counters
--   histogram  -> one row per point, Value = the bucket sum,
--                 Count = the observation count
--
-- This is enough to evaluate the threshold rules that drive health
-- (e.g. files_ready gauge > 100, or files_read sum rate < 1 over a
-- window). Exponential histograms and summaries, and full histogram
-- buckets, are intentionally NOT stored yet — they add a lot of schema
-- for no thresholding benefit today. Revisit if a widget needs them.
--
-- ORDER BY (ServiceName, MetricName, time) so "latest / aggregate of
-- metric M for service S over a window" hits the sort key.

CREATE TABLE IF NOT EXISTS metrics (
    Timestamp           DateTime64(9, 'UTC')                       CODEC(Delta, ZSTD(1)),
    StartTimestamp      DateTime64(9, 'UTC')                       CODEC(Delta, ZSTD(1)),
    MetricName          LowCardinality(String)                     CODEC(ZSTD(1)),
    MetricType          LowCardinality(String)                     CODEC(ZSTD(1)),
    ServiceName         LowCardinality(String)                     CODEC(ZSTD(1)),
    ServiceNamespace    LowCardinality(String)                     CODEC(ZSTD(1)),
    Value               Float64                                    CODEC(ZSTD(1)),
    Count               UInt64                                     CODEC(ZSTD(1)),
    IsMonotonic         UInt8                                      CODEC(ZSTD(1)),
    Unit                LowCardinality(String)                     CODEC(ZSTD(1)),
    ResourceAttributes  Map(LowCardinality(String), String)        CODEC(ZSTD(1)),
    MetricAttributes    Map(LowCardinality(String), String)        CODEC(ZSTD(1)),

    INDEX idx_resource_keys   mapKeys(ResourceAttributes)          TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_resource_values mapValues(ResourceAttributes)        TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_metric_keys     mapKeys(MetricAttributes)            TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_metric_values   mapValues(MetricAttributes)          TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_value           Value                                TYPE minmax              GRANULARITY 1
)
ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (ServiceName, MetricName, toUnixTimestamp(Timestamp))
TTL toDate(Timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;
