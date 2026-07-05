-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- OTLP logs schema. One row per OTel LogRecord. Mirrors the traces
-- table's conventions (Map attributes, ZSTD codecs, bloom-filter
-- indexes, daily partitions, 30-day TTL) so the two read the same way.
--
-- Body carries the rendered log message; a token bloom-filter index
-- makes substring/keyword search over it cheap without a full scan.
-- ServiceName leads the ORDER BY so "logs for this service over a
-- window" — the dominant query — hits the sort key.

CREATE TABLE IF NOT EXISTS logs (
    Timestamp           DateTime64(9, 'UTC')                       CODEC(Delta, ZSTD(1)),
    ObservedTimestamp   DateTime64(9, 'UTC')                       CODEC(Delta, ZSTD(1)),
    TraceId             String                                     CODEC(ZSTD(1)),
    SpanId              String                                     CODEC(ZSTD(1)),
    SeverityNumber      Int32                                      CODEC(ZSTD(1)),
    SeverityText        LowCardinality(String)                     CODEC(ZSTD(1)),
    ServiceName         LowCardinality(String)                     CODEC(ZSTD(1)),
    ServiceNamespace    LowCardinality(String)                     CODEC(ZSTD(1)),
    ScopeName           LowCardinality(String)                     CODEC(ZSTD(1)),
    Body                String                                     CODEC(ZSTD(1)),
    ResourceAttributes  Map(LowCardinality(String), String)        CODEC(ZSTD(1)),
    LogAttributes       Map(LowCardinality(String), String)        CODEC(ZSTD(1)),

    INDEX idx_trace_id        TraceId                              TYPE bloom_filter        GRANULARITY 4,
    INDEX idx_resource_keys   mapKeys(ResourceAttributes)          TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_resource_values mapValues(ResourceAttributes)        TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_log_keys        mapKeys(LogAttributes)               TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_log_values      mapValues(LogAttributes)             TYPE bloom_filter(0.01)  GRANULARITY 1,
    INDEX idx_body            Body                                 TYPE tokenbf_v1(32768, 3, 0) GRANULARITY 1
)
ENGINE = MergeTree()
PARTITION BY toDate(Timestamp)
ORDER BY (ServiceName, SeverityText, toUnixTimestamp(Timestamp))
TTL toDate(Timestamp) + INTERVAL 30 DAY
SETTINGS index_granularity = 8192;
