-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Maps are first-class transformations between two schemas: XSLT, jq,
-- JSONata, Liquid, Mustache, Handlebars, etc. Conceptually a map links
-- a "from" schema (input shape) to a "to" schema (output shape) and
-- carries the transformation source in `content` plus a `format`
-- discriminator. Previously these lived in the schemas catalogue under
-- kind='mapping' / kind='template'; pulling them out gives the model a
-- cleaner shape (Schemas = shape descriptions, Maps = transformations)
-- and lets the UI surface the from→to relationship directly.

CREATE TABLE IF NOT EXISTS maps (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL,
    -- Human identifier. Unique within (org, version) so the same map can
    -- carry multiple iterations side-by-side, matching the schemas
    -- versioning convention.
    name            TEXT NOT NULL,
    version         TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    -- The transformation language used in `content`. Free-text — the UI
    -- offers a curated set (xslt, jq, jsonata, liquid, mustache,
    -- handlebars, other) but anything is accepted. Defaults to xslt
    -- because it's the most common across BizTalk/SI migrations.
    format          TEXT NOT NULL DEFAULT 'xslt',
    -- The transformation source itself, stored as text. The UI displays
    -- it in CodeMirror with format-appropriate highlighting.
    content         TEXT NOT NULL DEFAULT '',
    -- Optional links to the input / output schemas. SET NULL on delete
    -- so removing a schema doesn't take maps down with it — they just
    -- become unlinked transformations until re-pointed.
    from_schema_id  UUID REFERENCES schemas(id) ON DELETE SET NULL,
    to_schema_id    UUID REFERENCES schemas(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name, version)
);
CREATE INDEX IF NOT EXISTS idx_maps_org_name ON maps(organization_id, name);
CREATE INDEX IF NOT EXISTS idx_maps_from_schema ON maps(from_schema_id) WHERE from_schema_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_maps_to_schema ON maps(to_schema_id) WHERE to_schema_id IS NOT NULL;
