-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- service_resource_attributes — per-service distinct resource-
-- attribute (key, value) tuples observed in recent telemetry.
--
-- Why: attribute-based access policies (group_access_policies.kind
-- IN ('attributes', 'compound')) need to answer "which services in
-- the catalog match these attribute constraints?" without a per-
-- request ClickHouse round-trip. The catalog reconciler samples
-- the spans table and upserts the distinct (svc, k, v) tuples here;
-- the policy resolver joins against this snapshot.
--
-- Trade-off: this is an eventually-consistent view. A service that
-- starts emitting `env=staging` for the first time at T0 won't be
-- visible to a policy filtering on `env=staging` until the next
-- reconciler tick. For a permissions surface, eventual consistency
-- on the order of seconds is fine — it errs on the side of "too
-- few" access, which is the safer direction.
--
-- We keep the table bounded by retaining the last_seen_at on each
-- row; the reconciler periodically prunes rows older than a few
-- days. Stale attributes a service no longer emits stop granting
-- visibility once they age out, which matches the intuition that
-- "if we haven't seen env=prod from this service in a week, it's
-- probably not a prod service any more."

CREATE TABLE IF NOT EXISTS service_resource_attributes (
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    attr_key     TEXT NOT NULL,
    attr_value   TEXT NOT NULL,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, service_name, attr_key, attr_value)
);
CREATE INDEX IF NOT EXISTS idx_sra_lookup ON service_resource_attributes(org_id, attr_key, attr_value);
CREATE INDEX IF NOT EXISTS idx_sra_freshness ON service_resource_attributes(last_seen_at);
