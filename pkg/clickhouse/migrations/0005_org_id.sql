-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- Tenant tag on telemetry. cell-ingest authenticates each OTLP batch by
-- per-org API key and stamps the resolved organization id onto every
-- row. Existing rows default to '' (empty = "untagged / single-tenant").
-- A bloom-filter skip index supports the Phase-2 read path that will
-- filter every query by OrganizationId for hard isolation.
ALTER TABLE traces  ADD COLUMN IF NOT EXISTS OrganizationId LowCardinality(String) DEFAULT '';
ALTER TABLE logs    ADD COLUMN IF NOT EXISTS OrganizationId LowCardinality(String) DEFAULT '';
ALTER TABLE metrics ADD COLUMN IF NOT EXISTS OrganizationId LowCardinality(String) DEFAULT '';
ALTER TABLE traces  ADD INDEX IF NOT EXISTS idx_org OrganizationId TYPE bloom_filter GRANULARITY 4;
ALTER TABLE logs    ADD INDEX IF NOT EXISTS idx_org OrganizationId TYPE bloom_filter GRANULARITY 4;
ALTER TABLE metrics ADD INDEX IF NOT EXISTS idx_org OrganizationId TYPE bloom_filter GRANULARITY 4;
