-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Reverse of 0016: widen the schemas CHECK and move maps back into
-- schemas as kind='mapping' (we lose the distinction between original
-- mapping vs template — best we can do without an extra column).
-- The from/to_schema links are dropped because the schemas table
-- has no place for them.

ALTER TABLE schemas DROP CONSTRAINT IF EXISTS schemas_kind_check;
ALTER TABLE schemas
    ADD CONSTRAINT schemas_kind_check
        CHECK (kind IN ('schema', 'template', 'mapping', 'example', 'other'));

INSERT INTO schemas (id, organization_id, name, kind, version,
                     description, format, content, created_at, updated_at)
SELECT id, organization_id, name, 'mapping', version,
       description, format, content, created_at, updated_at
FROM maps
ON CONFLICT (organization_id, name, version) DO NOTHING;

DELETE FROM maps;
