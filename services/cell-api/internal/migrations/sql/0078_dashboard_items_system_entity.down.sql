-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
DROP INDEX IF EXISTS dashboard_items_system_entity_uniq;
DELETE FROM dashboard_items WHERE entity_kind = 'system_entity';
ALTER TABLE dashboard_items DROP CONSTRAINT IF EXISTS chk_dashboard_item_shape;
ALTER TABLE dashboard_items
    ADD CONSTRAINT chk_dashboard_item_shape CHECK (
        (entity_kind = 'integration' AND integration_id IS NOT NULL AND system_name IS NULL)
     OR (entity_kind = 'system'      AND system_name IS NOT NULL AND integration_id IS NULL AND widget_type = 'system_health')
    );
ALTER TABLE dashboard_items DROP COLUMN IF EXISTS system_id;
