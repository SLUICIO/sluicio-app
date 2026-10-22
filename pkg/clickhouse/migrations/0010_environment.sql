-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- The environment a row was produced in (#40), derived once from the
-- resource attributes so filtering and grouping by environment does not
-- look up a map key per row.
--
-- OpenTelemetry's current key is deployment.environment.name. It replaced
-- deployment.environment in semantic conventions v1.27 and both are in
-- wide use, so the current one is read first. Empty when neither is set,
-- which reads as "(no environment)".
--
-- MATERIALIZED, so new rows get it at insert and existing parts compute
-- it on read until they are merged, and nothing is rewritten up front. A cell
-- configured to read another key, and value aliases (prd/prod ->
-- production), are applied at read time on top of this.
ALTER TABLE traces
    ADD COLUMN IF NOT EXISTS Environment LowCardinality(String)
        MATERIALIZED coalesce(nullIf(ResourceAttributes['deployment.environment.name'], ''), ResourceAttributes['deployment.environment'])
        CODEC(ZSTD(1));
ALTER TABLE logs
    ADD COLUMN IF NOT EXISTS Environment LowCardinality(String)
        MATERIALIZED coalesce(nullIf(ResourceAttributes['deployment.environment.name'], ''), ResourceAttributes['deployment.environment'])
        CODEC(ZSTD(1));
ALTER TABLE metrics
    ADD COLUMN IF NOT EXISTS Environment LowCardinality(String)
        MATERIALIZED coalesce(nullIf(ResourceAttributes['deployment.environment.name'], ''), ResourceAttributes['deployment.environment'])
        CODEC(ZSTD(1));
