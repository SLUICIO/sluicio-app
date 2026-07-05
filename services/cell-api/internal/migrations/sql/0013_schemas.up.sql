-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Data schemas (the shape of messages flowing between services) and
-- the per-service in / out links. With a service's In-Schema /
-- Out-Schema declared, Conduit can surface a new kind of dependency:
-- services that consume the same schema, or whose out-schema matches
-- another service's in-schema.

CREATE TABLE IF NOT EXISTS schemas (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL,
    -- Human identifier. Unique within an org; encode versioning in the
    -- name itself if you want it (e.g. "Order v1" vs "Order v2"). Keeps
    -- the model dead simple and avoids a parallel versions table.
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    -- Free-text discriminator for how the content should be parsed by
    -- the UI: 'json' (JSON Schema), 'yaml', 'protobuf', 'avro', etc.
    -- Defaults to 'json'; we don't enforce or validate the body.
    format          TEXT NOT NULL DEFAULT 'json',
    -- The schema itself, stored as text. The UI displays it
    -- monospaced; consumers can fetch + parse however they want.
    content         TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);
CREATE INDEX IF NOT EXISTS idx_schemas_org_name ON schemas(organization_id, name);

-- The link table. Direction is constrained so each service can carry
-- at most one in-schema and one out-schema; adding more later (e.g.
-- per-endpoint schemas) is a matter of widening the PK.
CREATE TABLE IF NOT EXISTS service_schemas (
    organization_id UUID NOT NULL,
    service_name    TEXT NOT NULL,
    direction       TEXT NOT NULL CHECK (direction IN ('in', 'out')),
    schema_id       UUID NOT NULL REFERENCES schemas(id) ON DELETE CASCADE,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, service_name, direction),
    -- A service must exist in the catalog before we can pin a schema
    -- on it. Deleting a service from the catalog cascades the link.
    FOREIGN KEY (organization_id, service_name)
        REFERENCES services(organization_id, service_name) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_service_schemas_schema_id ON service_schemas(schema_id);
