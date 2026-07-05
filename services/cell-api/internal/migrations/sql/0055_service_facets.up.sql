-- User-defined ("custom") service facets: classification labels an org can
-- create and assign to services (via facet overrides), alongside the built-in,
-- code-defined facets. Custom facets carry no widgets — they're labels for
-- grouping/filtering, not dashboard drivers.
CREATE TABLE service_facets (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    slug        TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, slug)
);
CREATE INDEX idx_service_facets_org ON service_facets(org_id);
