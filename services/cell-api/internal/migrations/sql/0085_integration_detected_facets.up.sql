-- Detected facets for an INTEGRATION, persisted.
--
-- Issue #26 stored facets per service and the integrations list rolled
-- them up: an integration's facets were the union of its members'. That
-- is wrong whenever a service belongs to more than one integration, and
-- it is wrong in the case that matters most.
--
-- An integration is a SLICE of its members' traffic. Membership comes
-- from the service matchers, but the attribute matchers narrow it
-- further — and narrowing a shared service between integrations is
-- precisely what those matchers are for:
--
--     Export to Lundify      service.name = nodered AND flow.id = tab_paperless
--     Monthly Archive        service.name = nodered AND flow.id = tab_paperless_monthly
--     Wordpress New Messages service.name = nodered AND flow.name = 'contact messages'
--
-- Three integrations, one member service, three different jobs. Rolling
-- up the service's facets gave all three the union of everything that
-- service does, so every integration carried "http input" as soon as any
-- flow on that runtime served HTTP. The facet column said the same thing
-- on every row, and the facet filter selected all of them or none —
-- exactly the question it exists to answer, answered wrongly.
--
-- So the classification is computed over the integration's own span
-- slice: its member services AND its matcher predicate, the same one the
-- message search applies. An integration is classified over exactly the
-- spans it shows you.
--
-- An integration with no attribute matchers narrows nothing, so its
-- slice is all of its members' traffic and the answer is identical to
-- the old rollup. One code path, no special case.
--
-- Expiry follows the same evidence-based rule as service_detected_facets
-- (see 0084): last_detected_at is bumped by every pass that still sees
-- the facet, and a facet is dropped only once it has not been seen for
-- longer than the telemetry retention. A monthly integration keeps its
-- classification between runs.

CREATE TABLE IF NOT EXISTS integration_detected_facets (
    organization_id   UUID        NOT NULL,
    integration_id    UUID        NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    facet_slug        TEXT        NOT NULL,
    last_detected_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    first_detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, integration_id, facet_slug)
);

-- The read path is "facets for these integrations".
CREATE INDEX IF NOT EXISTS idx_integration_detected_facets_integration
    ON integration_detected_facets (organization_id, integration_id);

-- Expiry sweeps by age across the whole org.
CREATE INDEX IF NOT EXISTS idx_integration_detected_facets_last_seen
    ON integration_detected_facets (organization_id, last_detected_at);
