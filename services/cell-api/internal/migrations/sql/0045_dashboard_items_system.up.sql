-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Let a dashboard item reference a System (a service flagged is_system) as
-- well as an Integration. A system item renders the 'system_health' widget
-- (status + error count + kind badge). entity_kind discriminates; an
-- integration item keeps integration_id, a system item carries system_name.

ALTER TABLE dashboard_items ADD COLUMN IF NOT EXISTS entity_kind TEXT NOT NULL DEFAULT 'integration';
ALTER TABLE dashboard_items ADD COLUMN IF NOT EXISTS system_name TEXT;
ALTER TABLE dashboard_items ALTER COLUMN integration_id DROP NOT NULL;

-- widget vocabulary gains system_health.
ALTER TABLE dashboard_items DROP CONSTRAINT IF EXISTS dashboard_items_widget_type_check;
ALTER TABLE dashboard_items
    ADD CONSTRAINT dashboard_items_widget_type_check
    CHECK (widget_type IN ('traffic_sparkline', 'error_count', 'latency_p95', 'system_health'));

-- Keep the polymorphic shape honest: integration items have an integration_id
-- and no system_name; system items have a system_name, no integration_id, and
-- the system_health widget.
ALTER TABLE dashboard_items
    ADD CONSTRAINT chk_dashboard_item_shape CHECK (
        (entity_kind = 'integration' AND integration_id IS NOT NULL AND system_name IS NULL)
     OR (entity_kind = 'system'      AND system_name IS NOT NULL AND integration_id IS NULL AND widget_type = 'system_health')
    );

-- The existing UNIQUE(dashboard_id, integration_id) now permits many system
-- rows (NULL integration_id is distinct in PG). Prevent duplicate systems
-- with a partial unique index on the system name.
CREATE UNIQUE INDEX IF NOT EXISTS dashboard_items_system_uniq
    ON dashboard_items (dashboard_id, system_name) WHERE system_name IS NOT NULL;
