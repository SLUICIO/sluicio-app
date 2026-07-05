-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- User-defined monitoring templates. The built-in templates stay code-defined
-- (read-only); these are org-owned custom templates a user can create from a
-- service's current health checks, fork from a built-in, or build by hand —
-- then apply like any template. `checks` is a JSON array mirroring the built-in
-- check spec (signal + metric/log fields). `source` is informational
-- provenance: 'custom' | 'fork:<kind>' | 'service:<name>'.

CREATE TABLE IF NOT EXISTS monitoring_templates (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'custom',
    checks      JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS monitoring_templates_org_idx ON monitoring_templates (org_id);
