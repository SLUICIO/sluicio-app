-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Span events (issue #42).
--
-- 0001 left events out with links, and 0008 brought only links back. An
-- exception is recorded by every OpenTelemetry SDK as a span EVENT named
-- "exception" carrying exception.type, exception.message and
-- exception.stacktrace - .NET's Activity.AddException, Java's
-- recordException and the rest. With events dropped at ingest, Sluicio
-- could show that a step failed but never why, for a product whose
-- subject is failed messages.
--
-- WHAT IS STORED
--
-- Per event its time, name and attributes, the layout of the
-- OpenTelemetry Collector's ClickHouse exporter (as parallel arrays, the
-- way 0008 stores links). Capped at 32 per span by the ingest side with
-- the true count in EventsTotal, and a stack trace truncated at 16 KB:
-- the median span has no events, and the cap is for the span that logs
-- thousands.
--
-- ExceptionType and ExceptionMessage are the span's FIRST exception,
-- derived from the events so failures can be grouped and filtered by
-- exception type without reading every event map.
--
-- MIGRATION SAFETY
--
-- ALTER ... ADD COLUMN is metadata-only on the largest table in the cell.
-- Existing rows get empty arrays, read as "not recorded" rather than "no
-- events": events sent before this migration were never stored.

ALTER TABLE traces
    ADD COLUMN IF NOT EXISTS EventsTimestamp  Array(DateTime64(9, 'UTC'))            CODEC(ZSTD(1)),
    ADD COLUMN IF NOT EXISTS EventsName       Array(LowCardinality(String))          CODEC(ZSTD(1)),
    ADD COLUMN IF NOT EXISTS EventsAttributes Array(Map(LowCardinality(String), String)) CODEC(ZSTD(1)),
    ADD COLUMN IF NOT EXISTS EventsTotal      UInt32                                 CODEC(ZSTD(1));

ALTER TABLE traces
    ADD COLUMN IF NOT EXISTS ExceptionType LowCardinality(String)
        MATERIALIZED if(has(EventsName, 'exception'), EventsAttributes[indexOf(EventsName, 'exception')]['exception.type'], '')
        CODEC(ZSTD(1)),
    ADD COLUMN IF NOT EXISTS ExceptionMessage String
        MATERIALIZED if(has(EventsName, 'exception'), EventsAttributes[indexOf(EventsName, 'exception')]['exception.message'], '')
        CODEC(ZSTD(1));
