-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- attribute_keys — distinct span + resource attribute KEYS (not values)
-- observed in recent telemetry, per org.
--
-- Why: integration attribute matchers (and the message-view filter
-- builder) let users pick an attribute to match on. The live source for
-- that picker is a capped ClickHouse sample of recent spans, so a
-- rarely-emitted attribute key can be missing from the list. The catalog
-- reconciler upserts the distinct keys here so the picker is instant and
-- complete (accumulated over time, not just a recent window).
--
-- Like service_resource_attributes (0021) this is an eventually-consistent
-- snapshot kept bounded by last_seen_at: the reconciler prunes keys not
-- seen for a couple of discovery windows, so attributes a fleet no longer
-- emits eventually age out of the picker.

CREATE TABLE IF NOT EXISTS attribute_keys (
    org_id        UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    attr_key      TEXT NOT NULL,
    source        TEXT NOT NULL DEFAULT 'span', -- 'span' | 'resource' (informational)
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, attr_key)
);
CREATE INDEX IF NOT EXISTS idx_attribute_keys_org ON attribute_keys(org_id);
CREATE INDEX IF NOT EXISTS idx_attribute_keys_freshness ON attribute_keys(last_seen_at);
