-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Initial traces schema. One row per OTel span. The layout follows the
-- conventions of the OpenTelemetry ClickHouse exporter and SigNoz so
-- that operators familiar with either feel at home, but is simplified
-- for our v0.1 needs (no Events / Links arrays yet).
--
-- The ORDER BY puts ServiceName and SpanName first so the most common
-- "all spans for a service" / "all spans of this kind" queries hit the
-- sort key and avoid a full scan. TraceId is last so that a known
-- trace can be retrieved cheaply.

CREATE TABLE IF NOT EXISTS traces (
    Timestamp           DateTime64(9, 'UTC')                       CODEC(Delta, ZSTD(1)),
    TraceId             String                                     CODEC(ZSTD(1)),
    SpanId              String                                     CODEC(ZSTD(1)),
    ParentSpanId        String                                     CODEC(ZSTD(1)),
    SpanName            LowCardinality(String)                     CODEC(ZSTD(1)),
    SpanKind            LowCardinality(String)                     CODEC(ZSTD(1)),
    ServiceName         LowCardinality(String)                     CODEC(ZSTD(1)),
    ServiceNamespace    LowCardinality(String)                     CODEC(ZSTD(1)),
    ResourceAttributes  Map(LowCardinality(String), String)        CODEC(ZSTD(1)),
    SpanAttributes      Map(LowCardinality(String), String)        CODEC(ZSTD(1)),
    DurationNs          UInt64                                     CODEC(ZSTD(1)),
    StatusCode          LowCardinality(String)                     CODEC(ZSTD(1)),
    StatusMessage       String                                     CODEC(ZSTD(1)),

    INDEX idx_trace_id        TraceId                              TYPE bloom_filter        GRANULARITY 4,
    INDEX idx_resource_keys   mapKeys(ResourceAttributes)          TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_resource_values mapValues(ResourceAttributes)        TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_span_keys       mapKeys(SpanAttributes)              TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_span_values     mapValues(SpanAttributes)            TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_duration        DurationNs                           TYPE minmax              GRANULARITY 1
)
ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (ServiceName, SpanName, toUnixTimestamp(Timestamp), TraceId)
TTL toDate(Timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;
