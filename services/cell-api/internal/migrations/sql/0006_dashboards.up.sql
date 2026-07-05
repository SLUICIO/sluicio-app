-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Dashboards: per-user, named, customizable layouts for the Home/Health
-- page. Each user can keep several dashboards (e.g. "Payments view",
-- "On-call view") and switch between them. Each dashboard either
-- auto-includes every integration in the org (today's behaviour) or
-- shows only the integrations the user explicitly picked. For each
-- integration the user can override which widget the card renders —
-- traffic sparkline (the v1 default), error count, or latency p95.
--
-- Ownership model mirrors message_views: organization-scoped with an
-- optional owner_user_id. NULL owner = org-shared / system default.
-- Until auth lands, owner_user_id stays NULL and the API treats every
-- dashboard as visible to the active org.

CREATE TABLE dashboards (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id      UUID NOT NULL,
    owner_user_id        UUID,
    name                 TEXT NOT NULL,
    -- One default per (org, owner). The API enforces single-default in
    -- the handler (a partial unique index would be ideal but PG can't
    -- treat NULL owners as distinct in a partial unique without a
    -- COALESCE; the handler check is simpler and good enough here).
    is_default           BOOLEAN NOT NULL DEFAULT FALSE,
    -- auto_include_all = TRUE  → render every integration in the org;
    --   items act as widget-type overrides.
    -- auto_include_all = FALSE → render only integrations listed in
    --   dashboard_items.
    auto_include_all     BOOLEAN NOT NULL DEFAULT TRUE,
    -- Widget type used when an integration has no explicit item row.
    -- Stored as text; vocabulary enforced by the API + CHECK below.
    default_widget_type  TEXT NOT NULL DEFAULT 'traffic_sparkline',
    -- Ordering in the dashboard picker. Lower comes first.
    position             INT NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (default_widget_type IN ('traffic_sparkline', 'error_count', 'latency_p95'))
);
CREATE INDEX dashboards_org_idx        ON dashboards (organization_id);
CREATE INDEX dashboards_org_owner_idx  ON dashboards (organization_id, owner_user_id);

-- A row pins an integration onto a dashboard and (optionally) overrides
-- the widget type for that one card. One row per (dashboard, integration)
-- so the UI's add/remove semantics map directly onto INSERT/DELETE.

CREATE TABLE dashboard_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    dashboard_id    UUID NOT NULL REFERENCES dashboards(id) ON DELETE CASCADE,
    integration_id  UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    widget_type     TEXT NOT NULL DEFAULT 'traffic_sparkline',
    -- Card ordering within the dashboard. Lower comes first; ties
    -- broken by created_at.
    position        INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (dashboard_id, integration_id),
    CHECK (widget_type IN ('traffic_sparkline', 'error_count', 'latency_p95'))
);
CREATE INDEX dashboard_items_dashboard_idx   ON dashboard_items (dashboard_id);
CREATE INDEX dashboard_items_integration_idx ON dashboard_items (integration_id);

-- Seed a single org-shared "All integrations" dashboard so a fresh
-- cell renders exactly the same view it does today (every integration,
-- traffic sparkline). New users land here until they create their own.
INSERT INTO dashboards
    (organization_id, owner_user_id, name, is_default, auto_include_all, default_widget_type, position)
VALUES
    ('00000000-0000-0000-0000-000000000001'::uuid,
     NULL, 'All integrations', TRUE, TRUE, 'traffic_sparkline', 0);
