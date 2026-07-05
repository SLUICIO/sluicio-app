-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Service custom metrics: external observations (e.g. queue depth)
-- pushed in by scrapers, with a threshold rule that drives the
-- service's health status. The metric definition and its current
-- value live in two tables so values can be updated cheaply without
-- rewriting the definition.

CREATE TYPE custom_metric_operator AS ENUM ('gt', 'gte', 'lt', 'lte');

CREATE TABLE service_custom_metrics (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      UUID NOT NULL,
    service_name         TEXT NOT NULL,
    slug                 TEXT NOT NULL,
    name                 TEXT NOT NULL,
    description          TEXT,
    threshold_operator   custom_metric_operator NOT NULL,
    threshold_value      DOUBLE PRECISION NOT NULL,
    unit                 TEXT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, service_name, slug)
);
CREATE INDEX service_custom_metrics_lookup_idx
    ON service_custom_metrics (organization_id, service_name);

-- A single latest-value row per metric. We deliberately don't keep a
-- history table in v1; if a customer wants a chart of historical
-- depth they should feed the data into ClickHouse via the OTLP
-- metrics path (once it exists).
CREATE TABLE service_custom_metric_values (
    metric_id      UUID PRIMARY KEY REFERENCES service_custom_metrics(id) ON DELETE CASCADE,
    value          DOUBLE PRECISION NOT NULL,
    observed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
