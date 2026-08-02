-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Let a dashboard item reference a SYSTEM ENTITY — a row in `systems`,
-- with members and its own health checks — and not only a service that
-- happens to be flagged is_system.
--
-- Those are different things, which is why this is a third entity_kind
-- rather than a reinterpretation of the existing one. 0045's 'system'
-- kind stores a service NAME and renders that service's health; the
-- entity has an id, spans several services, and carries checks bound to
-- itself. Migrating the old rows would mean guessing which entity a
-- flagged service belongs to, and it may belong to none — so the old
-- kind keeps working untouched and the picker offers both.
--
-- ON DELETE CASCADE: a card pointing at a deleted system is not a card,
-- and leaving a dangling row would render an empty tile no one can
-- remove from the dashboard UI.

ALTER TABLE dashboard_items
    ADD COLUMN IF NOT EXISTS system_id UUID REFERENCES systems(id) ON DELETE CASCADE;

-- Widen the shape constraint to admit the new kind. Same discipline as
-- 0045: exactly one target column is populated per kind, so a malformed
-- row cannot be written and later render as a blank card.
ALTER TABLE dashboard_items DROP CONSTRAINT IF EXISTS chk_dashboard_item_shape;
ALTER TABLE dashboard_items
    ADD CONSTRAINT chk_dashboard_item_shape CHECK (
        (entity_kind = 'integration'   AND integration_id IS NOT NULL AND system_name IS NULL AND system_id IS NULL)
     OR (entity_kind = 'system'        AND system_name IS NOT NULL AND integration_id IS NULL AND system_id IS NULL AND widget_type = 'system_health')
     OR (entity_kind = 'system_entity' AND system_id IS NOT NULL AND integration_id IS NULL AND system_name IS NULL AND widget_type = 'system_health')
    );

-- One card per system per dashboard, mirroring the name-keyed index 0045
-- added for the older kind.
CREATE UNIQUE INDEX IF NOT EXISTS dashboard_items_system_entity_uniq
    ON dashboard_items (dashboard_id, system_id) WHERE system_id IS NOT NULL;
