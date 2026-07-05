-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- System metadata (docs/systems.md phase 3). Metadata fields gain a third
-- scope — systems — optionally narrowed to one system type. A field with
-- applies_to_system applies to every system; with system_type_key set, only to
-- systems of that type. Values are stored per system in system_metadata.

ALTER TABLE metadata_fields
    ADD COLUMN IF NOT EXISTS applies_to_system BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS system_type_key   TEXT    NOT NULL DEFAULT '';

-- Replace the old "must have a scope" check so a system-only field is valid.
ALTER TABLE metadata_fields DROP CONSTRAINT IF EXISTS metadata_fields_check;
ALTER TABLE metadata_fields
    ADD CONSTRAINT metadata_fields_scope_check
    CHECK (applies_to_integration OR applies_to_service OR applies_to_system);

-- Values attached to a system.
CREATE TABLE IF NOT EXISTS system_metadata (
    system_id  UUID NOT NULL REFERENCES systems(id) ON DELETE CASCADE,
    field_id   UUID NOT NULL REFERENCES metadata_fields(id) ON DELETE CASCADE,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (system_id, field_id)
);
CREATE INDEX IF NOT EXISTS idx_system_metadata_field ON system_metadata(field_id);
