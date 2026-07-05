-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Reverse 0045: drop system items, restore the integration-only shape.

DELETE FROM dashboard_items WHERE entity_kind = 'system';

DROP INDEX IF EXISTS dashboard_items_system_uniq;
ALTER TABLE dashboard_items DROP CONSTRAINT IF EXISTS chk_dashboard_item_shape;
ALTER TABLE dashboard_items DROP CONSTRAINT IF EXISTS dashboard_items_widget_type_check;
ALTER TABLE dashboard_items
    ADD CONSTRAINT dashboard_items_widget_type_check
    CHECK (widget_type IN ('traffic_sparkline', 'error_count', 'latency_p95'));

ALTER TABLE dashboard_items ALTER COLUMN integration_id SET NOT NULL;
ALTER TABLE dashboard_items DROP COLUMN IF EXISTS system_name;
ALTER TABLE dashboard_items DROP COLUMN IF EXISTS entity_kind;
