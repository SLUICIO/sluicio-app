-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- cell_settings: a tiny key/value store for cell-wide knobs that don't
-- justify their own table. JSON value gives schema flexibility for
-- whatever a setting needs to carry; updated_at lets the consuming
-- service notice changes since the last read.
--
-- Why a single key/value table rather than a typed column per setting:
-- the set of cell-wide settings is going to grow (retention now, query
-- timeouts next, default time windows after that, sampling rates
-- maybe). Adding a column per knob would mean a migration for every
-- new setting. The key/value shape doesn't.
--
-- These settings are cell-level, not org-level. Sluicio's telemetry
-- tables (spans/logs/metrics) live in a shared ClickHouse database
-- with no org_id column — tenancy in v1 is via resource attribute
-- labelling, not schema isolation. A multi-org cell therefore shares
-- one retention policy. When/if we move to per-org physical tables,
-- this table grows an org_id and the unique key becomes (org_id, key).

CREATE TABLE IF NOT EXISTS cell_settings (
    key         TEXT PRIMARY KEY,
    value       JSONB NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed defaults for telemetry retention. 14 days for everything to
-- start — matches the "two-week rolling window" baseline most users
-- expect. Operators can dial each independently:
--
--   telemetry.retention.traces    — span / trace data
--   telemetry.retention.logs      — log records
--   telemetry.retention.metrics   — metric points
--
-- The value shape is {"days": N} so it serialises cleanly to JSON and
-- leaves room for future qualifiers (e.g. {"days": N, "downsample_after_days": M}).
INSERT INTO cell_settings (key, value, description) VALUES
    ('telemetry.retention.traces',
     '{"days": 14}'::jsonb,
     'How long span / trace data is kept before ClickHouse TTL drops it.'),
    ('telemetry.retention.logs',
     '{"days": 14}'::jsonb,
     'How long log records are kept before ClickHouse TTL drops them.'),
    ('telemetry.retention.metrics',
     '{"days": 14}'::jsonb,
     'How long metric points are kept before ClickHouse TTL drops them. Often raised to 14 months or more for capacity planning.')
ON CONFLICT (key) DO NOTHING;

-- We also record when the enforcer last applied each setting to
-- ClickHouse. Stored as a separate setting so the read path is one
-- table; the enforcer writes through here.
INSERT INTO cell_settings (key, value, description) VALUES
    ('telemetry.retention.last_applied',
     '{}'::jsonb,
     'Map of {telemetry_type: ISO timestamp} for the last time the retention enforcer pushed this setting into ClickHouse TTL. Written by the cell-api retention loop.')
ON CONFLICT (key) DO NOTHING;
