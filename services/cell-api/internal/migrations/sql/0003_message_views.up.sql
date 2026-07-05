-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Message views: persisted saved searches for the "Messages" page.
-- Each view is a named bundle of structured filters (field / operator
-- / value triplets) that the frontend's FilterEditor produces and
-- consumes. Filters are stored as JSONB so we can iterate on the
-- filter shape without a schema migration each time.
--
-- v1 ownership model: views are scoped to an organization, with an
-- optional owner_user_id. A view is "mine" if its owner matches the
-- caller; a view is visible team-wide when shared = TRUE. Until auth
-- and user identity land, owner_user_id stays NULL and the API treats
-- all views as visible to the active org.

CREATE TABLE message_views (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id    UUID NOT NULL,
    owner_user_id      UUID,
    name               TEXT NOT NULL,
    description        TEXT,
    pinned             BOOLEAN NOT NULL DEFAULT FALSE,
    shared             BOOLEAN NOT NULL DEFAULT TRUE,
    -- filters is a JSON array of filter objects:
    --   [{"field":"payload","field_path":"orderId","op":"equals","value":"1323"}]
    -- Validated by the API; the DB does not constrain the shape.
    filters            JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- Optional cached cardinality from the most recent run. Surfaces
    -- on the saved-views rail without re-running the query.
    last_result_count  BIGINT,
    last_edited_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX message_views_org_idx ON message_views (organization_id);
CREATE INDEX message_views_org_owner_idx ON message_views (organization_id, owner_user_id);

-- Seed a couple of stock views so a fresh cell already has something
-- on the rail. Mirrors the UI's previous in-memory seed.
INSERT INTO message_views (organization_id, name, pinned, shared, filters)
VALUES
    ('00000000-0000-0000-0000-000000000001'::uuid,
     'all errors · today', TRUE, TRUE,
     '[{"field":"status","op":"is","value":"err only"},
       {"field":"time","op":"is","value":"last 24 hours"}]'::jsonb),
    ('00000000-0000-0000-0000-000000000001'::uuid,
     'slow > 2s', FALSE, TRUE,
     '[{"field":"time","op":"is","value":"last 24 hours"}]'::jsonb);
