-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Two additions to schemas now that the catalogue holds more than
-- JSON-Schema-shaped things:
--
--   - kind: what category of artifact is this? "schema" still covers
--     JSON Schema / OpenAPI / Avro / Protobuf, but liquid templates,
--     XSLT, and example documents all belong here too — distinguishing
--     them up-front avoids users having to guess from the name.
--   - version: lightweight evolution. The old unique constraint on
--     (org, name) becomes (org, name, version) so the same conceptual
--     schema can carry multiple versions side by side.

ALTER TABLE schemas
    ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'schema'
        CHECK (kind IN ('schema', 'template', 'mapping', 'example', 'other')),
    ADD COLUMN IF NOT EXISTS version TEXT NOT NULL DEFAULT '';

-- Replace the old (org, name) uniqueness with one that includes
-- version. IF EXISTS is used so down/up cycles are forgiving.
ALTER TABLE schemas
    DROP CONSTRAINT IF EXISTS schemas_organization_id_name_key;
ALTER TABLE schemas
    ADD CONSTRAINT schemas_organization_id_name_version_key
        UNIQUE (organization_id, name, version);
