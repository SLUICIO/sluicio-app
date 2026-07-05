-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
DROP TABLE IF EXISTS system_metadata;
ALTER TABLE metadata_fields DROP CONSTRAINT IF EXISTS metadata_fields_scope_check;
ALTER TABLE metadata_fields
    ADD CONSTRAINT metadata_fields_check
    CHECK (applies_to_integration OR applies_to_service);
ALTER TABLE metadata_fields
    DROP COLUMN IF EXISTS applies_to_system,
    DROP COLUMN IF EXISTS system_type_key;
