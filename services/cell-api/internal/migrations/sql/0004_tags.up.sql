-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Tags: a flat, org-scoped vocabulary that can be attached to
-- integrations and to individual services. Typical examples are
-- department ("HR", "Finance"), environment ("prod", "staging"), or
-- owner-team labels. The v1 tag has only a name, a stable slug, and a
-- display color — categories / namespaces / descriptions can be added
-- later without breaking storage.
--
-- Integration tags and service tags are stored in separate join tables
-- on purpose:
--   - integration_tags references the integrations row (FK, cascades).
--   - service_tags is keyed by (organization_id, service_name) because
--     services are not first-class rows in this DB — they are
--     discovered from telemetry. A service tag persists across the
--     service going quiet and coming back, which is what users want.

CREATE TABLE tags (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  UUID NOT NULL,
    slug             TEXT NOT NULL,
    name             TEXT NOT NULL,
    -- Lowercase hex color, "#rgb" or "#rrggbb". Validated at the API
    -- layer too; the CHECK is a backstop against direct SQL writes.
    color            TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, slug),
    CHECK (color ~ '^#([0-9a-f]{3}|[0-9a-f]{6})$')
);
CREATE INDEX tags_org_idx ON tags (organization_id);

-- Integrations <-> tags. ON DELETE CASCADE on both sides so removing
-- either end cleans up the link.
CREATE TABLE integration_tags (
    integration_id  UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    tag_id          UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (integration_id, tag_id)
);
CREATE INDEX integration_tags_tag_idx ON integration_tags (tag_id);

-- Service tags. Keyed by (org, service_name) rather than a service FK
-- because services live in ClickHouse, not Postgres. Deletes cascade
-- from the tag side; orphaned (service_name no longer in telemetry)
-- rows are harmless and can be reaped by a future janitor.
CREATE TABLE service_tags (
    organization_id  UUID NOT NULL,
    service_name     TEXT NOT NULL,
    tag_id           UUID NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, service_name, tag_id)
);
CREATE INDEX service_tags_tag_idx ON service_tags (tag_id);
CREATE INDEX service_tags_org_service_idx ON service_tags (organization_id, service_name);
