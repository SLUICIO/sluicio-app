-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Reverse 0034: restore the service_custom_metrics schema (empty — the
-- folded-in rows are NOT moved back) and drop the unification columns.
-- A real rollback also requires reverting the cell-api code, since the
-- custommetrics package is removed in the same change.

CREATE TYPE custom_metric_operator AS ENUM ('gt', 'gte', 'lt', 'lte');

CREATE TABLE IF NOT EXISTS service_custom_metrics (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      UUID NOT NULL,
    service_name         TEXT NOT NULL,
    slug                 TEXT NOT NULL,
    name                 TEXT NOT NULL,
    description          TEXT,
    threshold_operator   custom_metric_operator NOT NULL,
    threshold_value      DOUBLE PRECISION NOT NULL,
    unit                 TEXT,
    source               TEXT NOT NULL DEFAULT 'pushed',
    metric_name          TEXT,
    aggregation          TEXT,
    attrs                JSONB NOT NULL DEFAULT '[]'::jsonb,
    window_seconds       INTEGER NOT NULL DEFAULT 300,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, service_name, slug)
);
CREATE INDEX IF NOT EXISTS service_custom_metrics_lookup_idx
    ON service_custom_metrics (organization_id, service_name);

CREATE TABLE IF NOT EXISTS service_custom_metric_values (
    metric_id      UUID PRIMARY KEY REFERENCES service_custom_metrics(id) ON DELETE CASCADE,
    value          DOUBLE PRECISION NOT NULL,
    observed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

DROP TABLE IF EXISTS alert_rule_readings;

ALTER TABLE alert_rules
    DROP COLUMN IF EXISTS source,
    DROP COLUMN IF EXISTS display_on_service,
    DROP COLUMN IF EXISTS unit;
