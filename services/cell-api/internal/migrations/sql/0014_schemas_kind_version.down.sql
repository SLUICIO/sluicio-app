-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
ALTER TABLE schemas DROP CONSTRAINT IF EXISTS schemas_organization_id_name_version_key;
ALTER TABLE schemas ADD CONSTRAINT schemas_organization_id_name_key UNIQUE (organization_id, name);
ALTER TABLE schemas DROP COLUMN IF EXISTS version;
ALTER TABLE schemas DROP COLUMN IF EXISTS kind;
