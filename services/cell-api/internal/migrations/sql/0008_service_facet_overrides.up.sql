-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Manual service-facet overrides: per-service deltas on top of the
-- facets the cell-api auto-detects from telemetry. Unlike
-- service_facet_mappings (which translate other span attributes into
-- io.kind / io.role so detection still runs off the data), these are a
-- direct human decision for cases the OTLP data can't express at all.
--
--   action = 'include'  -> force the facet ON  even if not auto-detected
--   action = 'exclude'  -> force the facet OFF even if auto-detected
--
-- The effective facet set for a service is therefore:
--   (auto-detected ∪ includes) − excludes
--
-- Storing deltas rather than a snapshot keeps the override
-- window-independent: an exclude only bites when the facet would
-- otherwise auto-appear, and an include always adds. facet_slug is the
-- registry slug (file-input, queue-output, worker, …); the API layer
-- validates it against the built-in registry so a typo can't strand a
-- row pointing at a non-existent facet.
--
-- Keyed by (organization_id, service_name) like service_facet_mappings
-- and service_tags, because services live in ClickHouse, not Postgres.

CREATE TYPE service_facet_override_action AS ENUM ('include', 'exclude');

CREATE TABLE service_facet_overrides (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   UUID NOT NULL,
    service_name      TEXT NOT NULL,
    facet_slug        TEXT NOT NULL,
    action            service_facet_override_action NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- At most one override per (service, facet): you can't both include
    -- and exclude the same facet. The PUT handler replaces the whole set
    -- for a service, so this also makes re-saves idempotent.
    UNIQUE (organization_id, service_name, facet_slug)
);

-- The cell-api fetches all overrides for a specific service whenever it
-- classifies that service or computes its widgets — a tight index over
-- the lookup key keeps it cheap even with many services.
CREATE INDEX service_facet_overrides_lookup_idx
    ON service_facet_overrides (organization_id, service_name);
