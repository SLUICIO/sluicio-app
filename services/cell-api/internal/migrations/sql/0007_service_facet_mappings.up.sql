-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Service facet attribute mappings: user-defined rules that say
-- "for this service, treat spans where attribute X satisfies condition Y
--  as carrying io.kind=K and io.role=R".
--
-- The cell-api uses these rules to derive effective io.kind / io.role
-- expressions when classifying a service into facets (file-input,
-- queue-output, etc.) and when computing per-facet widget metrics.
-- Without rules, classification falls back to the raw SpanAttributes
-- the service emits — so legacy services that don't yet set io.kind /
-- io.role can be brought into the facet model via the UI instead of
-- requiring re-instrumentation.
--
-- Keyed by (organization_id, service_name) rather than a FK to a
-- services row, because services live in ClickHouse, not Postgres —
-- the same reason service_tags uses the same pattern.

CREATE TYPE facet_mapping_attr_source AS ENUM ('span', 'resource');

-- 'exists' has no value (the row's match_value is empty). The other
-- ops compare match_value against the attribute lexically. Regex is
-- deliberately omitted from v1 — the additional query-injection
-- surface isn't worth the small UX gain right now.
CREATE TYPE facet_mapping_operator AS ENUM ('equals', 'prefix', 'suffix', 'contains', 'exists');

CREATE TABLE service_facet_mappings (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   UUID NOT NULL,
    service_name      TEXT NOT NULL,
    attribute_source  facet_mapping_attr_source NOT NULL,
    attribute_key     TEXT NOT NULL,
    match_operator    facet_mapping_operator NOT NULL,
    -- Empty string when match_operator='exists'; otherwise the value
    -- the attribute is compared against. The CHECK enforces this
    -- contract so a buggy writer can't store an inconsistent row.
    match_value       TEXT NOT NULL DEFAULT '',
    -- The io.kind / io.role this rule should impute when it matches.
    -- Validated at the API layer against the closed set used by the
    -- built-in facets (file, queue, http, db, email and input/output).
    set_io_kind       TEXT NOT NULL,
    set_io_role       TEXT NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (match_operator = 'exists' OR match_value <> ''),
    CHECK (set_io_kind <> '' AND set_io_role <> '')
);

-- The cell-api fetches all mappings for a specific service every time
-- it computes widgets or runs classification — small N per service,
-- but a tight index keeps it cheap even with many services.
CREATE INDEX service_facet_mappings_lookup_idx
    ON service_facet_mappings (organization_id, service_name);
