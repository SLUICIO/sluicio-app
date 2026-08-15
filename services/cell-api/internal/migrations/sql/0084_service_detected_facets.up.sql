-- Detected service facets, persisted (issue #26).
--
-- Facets say what KIND of work a service does. They were derived live
-- from whatever time window the reader had selected, which made a
-- service's identity flicker: "HTTP input" on 24 hours, unclassified on
-- one hour, with nothing about the service having changed. Worse, the
-- empty result was indistinguishable from a real answer — a blank
-- column reads as "no facets", not as "nothing in this window to look
-- at". A monthly flow looked unclassified most of the time.
--
-- A facet is a property of the service, evidenced by telemetry. So it
-- is stored, refreshed on a schedule, and read from here.
--
-- EXPIRY IS EVIDENCE-BASED, which is the whole design decision.
--
-- last_detected_at is bumped every pass that still sees the facet. A
-- facet is dropped only when it has not been seen for longer than the
-- telemetry retention — i.e. when the evidence for it can no longer
-- exist in ClickHouse. The rule states in one sentence: the
-- classification lasts as long as the data behind it.
--
-- The alternatives were worse in specific ways. Never expiring keeps
-- retired boundaries for ever. Recomputing wholesale each pass over a
-- fixed window drops a monthly flow's facets between its runs, which is
-- the case that prompted this.
--
-- Only AUTO-DETECTED facets live here. Manual assignments already have
-- their own table (service_facet_overrides) and are layered on top at
-- read time, unchanged.

CREATE TABLE IF NOT EXISTS service_detected_facets (
    organization_id  UUID        NOT NULL,
    service_name     TEXT        NOT NULL,
    facet_slug       TEXT        NOT NULL,
    -- When a detection pass last saw evidence for this facet. Drives
    -- expiry, and lets the UI answer "why does this say HTTP input?"
    -- with "because it did, as recently as …".
    last_detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    first_detected_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, service_name, facet_slug)
);

-- The read path is "facets for these services", so the service is the
-- leading edge after the org.
CREATE INDEX IF NOT EXISTS idx_detected_facets_service
    ON service_detected_facets (organization_id, service_name);

-- Expiry sweeps by age across the whole org.
CREATE INDEX IF NOT EXISTS idx_detected_facets_last_seen
    ON service_detected_facets (organization_id, last_detected_at);
