-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Typed defaults for the span event columns (fix for 0009).
--
-- 0009 added the event arrays without a default. For a part written
-- before it, ClickHouse fills a missing array column with an untyped
-- empty array (Array(Nothing)), and the MATERIALIZED ExceptionType /
-- ExceptionMessage, computed on read for such parts, fail on it:
-- "Cannot create non-empty column with type Nothing". Every read of those
-- columns over a window reaching data from before the upgrade failed,
-- the message list among them.
--
-- A typed default is what a missing column is filled with, so this fixes
-- old parts without rewriting them. Metadata only.
ALTER TABLE traces
    MODIFY COLUMN EventsTimestamp  Array(DateTime64(9, 'UTC'))                 DEFAULT CAST([], 'Array(DateTime64(9, \'UTC\'))') CODEC(ZSTD(1)),
    MODIFY COLUMN EventsName       Array(LowCardinality(String))               DEFAULT CAST([], 'Array(LowCardinality(String))') CODEC(ZSTD(1)),
    MODIFY COLUMN EventsAttributes Array(Map(LowCardinality(String), String)) DEFAULT CAST([], 'Array(Map(LowCardinality(String), String))') CODEC(ZSTD(1));
