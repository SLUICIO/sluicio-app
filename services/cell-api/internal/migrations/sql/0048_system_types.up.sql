-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- System-types catalog. A "system type" (rabbitmq, kafka, otel-collector, …)
-- owns its detection prefixes (auto-identify the type from a service's emitted
-- metric names) and its starter health checks (its monitoring template). The
-- built-in catalog stays code-defined and read-only; these rows are an org's
-- CUSTOM types plus OVERRIDES of a built-in (an org row whose `key` matches a
-- built-in replaces it for that org). `detect_prefixes` and `checks` are JSON
-- arrays; `checks` mirrors the built-in check spec (signal + metric/log fields).

CREATE TABLE IF NOT EXISTS system_types (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL,
    key             TEXT NOT NULL,
    label           TEXT NOT NULL,
    is_system       BOOLEAN NOT NULL DEFAULT false,
    detect_prefixes JSONB NOT NULL DEFAULT '[]'::jsonb,
    checks          JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per (org, key): an org has at most one custom/override per type key.
CREATE UNIQUE INDEX IF NOT EXISTS system_types_org_key_idx ON system_types (org_id, key);
