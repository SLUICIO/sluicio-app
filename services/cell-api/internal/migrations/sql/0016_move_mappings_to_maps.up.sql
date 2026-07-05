-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Data migration: anything that lived in `schemas` under kind='mapping'
-- or kind='template' moves into the new `maps` table created by 0015.
-- The schemas CHECK constraint is then tightened to drop those kinds so
-- new rows can't slip back in via the API.
--
-- The data move is idempotent on (org, name, version) — the unique
-- constraint on maps would reject duplicates, so we use ON CONFLICT DO
-- NOTHING for the rare case where someone has already created a Map
-- with the same identity (e.g. partial re-runs of this migration).

-- 1) Copy mapping / template schemas into maps. from/to schema links
--    are NULL initially — the user re-points them on first edit. We
--    preserve id where possible so any external bookmark / API caller
--    can still resolve it after the move.
INSERT INTO maps (id, organization_id, name, version, description, format, content,
                  from_schema_id, to_schema_id, created_at, updated_at)
SELECT id, organization_id, name, version, description, format, content,
       NULL, NULL, created_at, updated_at
FROM schemas
WHERE kind IN ('mapping', 'template')
ON CONFLICT (organization_id, name, version) DO NOTHING;

-- 2) Tear down any service_schemas rows that pointed at moved schemas.
--    Maps don't carry an in/out service link in this design, so those
--    rows would become orphaned by FK semantics anyway. We do this
--    explicitly so the cascade in step 3 is empty and the log is clean.
DELETE FROM service_schemas
WHERE schema_id IN (
    SELECT id FROM schemas WHERE kind IN ('mapping', 'template')
);

-- 3) Drop the moved rows from schemas.
DELETE FROM schemas WHERE kind IN ('mapping', 'template');

-- 4) Tighten the kind CHECK so 'mapping' / 'template' can't be written
--    via the API any more. CHECK constraints can't be altered in place
--    — drop and re-add. The constraint name follows the historic
--    `schemas_kind_check` convention Postgres auto-assigns to inline
--    CHECKs on the kind column.
ALTER TABLE schemas DROP CONSTRAINT IF EXISTS schemas_kind_check;
ALTER TABLE schemas
    ADD CONSTRAINT schemas_kind_check
        CHECK (kind IN ('schema', 'example', 'other'));
