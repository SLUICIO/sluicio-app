-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Persisted services catalog + materialised integration membership.
-- Services are still discovered from telemetry (ClickHouse traces) —
-- this is the persistent projection of that discovery into Postgres,
-- kept fresh by a background reconciler in cell-api. Two benefits:
--
--   1. The integration → services link is queryable from SQL and
--      stable across empty time windows (a service that's quiet today
--      is still listed as a member as long as the reconciler has it).
--   2. The first_seen_at / last_seen_at columns give us a real
--      "discovered at" timestamp that doesn't depend on ClickHouse
--      retention.

CREATE TABLE IF NOT EXISTS services (
    organization_id   UUID NOT NULL,
    service_name      TEXT NOT NULL,
    service_namespace TEXT NOT NULL DEFAULT '',
    first_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, service_name)
);
CREATE INDEX IF NOT EXISTS idx_services_org_last_seen ON services(organization_id, last_seen_at DESC);

-- Materialised matcher membership. The reconciler rewrites this for
-- each integration whenever its matchers change or when a new service
-- appears that one of the matchers selects.
CREATE TABLE IF NOT EXISTS integration_services (
    integration_id  UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL,
    service_name    TEXT NOT NULL,
    matched_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (integration_id, service_name)
);
CREATE INDEX IF NOT EXISTS idx_integration_services_org_service
    ON integration_services(organization_id, service_name);
