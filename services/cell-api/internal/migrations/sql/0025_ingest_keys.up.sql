-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- Per-organization ingest API keys. A collector presents one of these
-- (Authorization: Bearer <key>) to cell-ingest; the resolved org is
-- stamped onto every telemetry row. Telemetry with no valid key is
-- bounced.
--
-- key_hash is a hex SHA-256 of the full key (NOT argon2id like
-- api_tokens): the key is high-entropy random, and cell-ingest must
-- verify it on every OTLP batch, so a fast hash + in-memory cache is
-- the right trade-off. prefix is the first chars kept in plaintext so
-- the UI can show "slk_a1b2c3d4…" without recovering the key.

CREATE TABLE IF NOT EXISTS ingest_keys (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    prefix          TEXT NOT NULL,
    key_hash        TEXT NOT NULL UNIQUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);

-- cell-ingest looks keys up by hash, filtered to live keys.
CREATE INDEX IF NOT EXISTS ingest_keys_hash_idx
    ON ingest_keys (key_hash) WHERE revoked_at IS NULL;

-- The settings UI lists a single org's keys.
CREATE INDEX IF NOT EXISTS ingest_keys_org_idx
    ON ingest_keys (organization_id);
