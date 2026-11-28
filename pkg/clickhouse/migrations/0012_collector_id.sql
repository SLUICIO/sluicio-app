-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- The collector a row came through (collector management). A collector
-- Sluicio manages can stamp the data it carries with its own id
-- (resource attribute sluicio.collector.id, set by the first collector on
-- the way). cell-ingest takes the stamp out of the resource attributes
-- and writes it here, so it never becomes part of a series' identity: a
-- counter arriving alternately through two load-balanced gateways would
-- otherwise split into two series, and a rate over them would overcount.
--
-- Empty for data that was not stamped, which is all data written before
-- this migration; the typed default is what such parts read as.
ALTER TABLE traces
    ADD COLUMN IF NOT EXISTS CollectorId LowCardinality(String) DEFAULT CAST('', 'LowCardinality(String)') CODEC(ZSTD(1));
ALTER TABLE logs
    ADD COLUMN IF NOT EXISTS CollectorId LowCardinality(String) DEFAULT CAST('', 'LowCardinality(String)') CODEC(ZSTD(1));
ALTER TABLE metrics
    ADD COLUMN IF NOT EXISTS CollectorId LowCardinality(String) DEFAULT CAST('', 'LowCardinality(String)') CODEC(ZSTD(1));
