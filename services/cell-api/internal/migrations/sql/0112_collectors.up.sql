-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Collector management over OpAMP, stage 1 (read only).
--
-- A customer's OpenTelemetry Collectors connect to the cell's OpAMP
-- endpoint and report who they are, what they can do, their health and
-- the configuration they actually run. Every collector belongs to exactly
-- one team: the team whose token it connected with.

-- collector_tokens: what a collector presents in its Authorization
-- header. The token IS the team assignment, so a team can hold several at
-- once (rotate without an outage: create the new one, switch, revoke the
-- old). Only a SHA-256 hash is stored; the plaintext is shown once.
-- Tokens go with their team.
CREATE TABLE IF NOT EXISTS collector_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    group_id        UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    prefix          TEXT NOT NULL,
    token_hash      TEXT NOT NULL UNIQUE,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_collector_tokens_group ON collector_tokens(group_id) WHERE revoked_at IS NULL;

-- collectors: the agent registry. instance_uid is chosen by the agent and
-- is not a security property, so it is unique only within a team: the
-- same collector connecting with another team's token becomes a separate
-- agent there and can never overwrite this team's row.
--
-- group_id has no ON DELETE action on purpose: a team that still owns
-- collectors cannot be deleted (no team-less collectors, and deleting a
-- team must not silently drop configuration history). NO ACTION rather
-- than RESTRICT so deleting the whole organization, which cascades to both
-- tables in one statement, still works.
--
-- kind is derived from the capabilities the agent reports: 'supervisor'
-- accepts remote configuration, 'extension' only reports.
CREATE TABLE IF NOT EXISTS collectors (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id   UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    group_id          UUID NOT NULL REFERENCES groups(id),
    instance_uid      TEXT NOT NULL,
    kind              TEXT NOT NULL DEFAULT 'extension' CHECK (kind IN ('extension', 'supervisor')),
    capabilities      BIGINT NOT NULL DEFAULT 0,
    description       JSONB NOT NULL DEFAULT '{}'::jsonb,
    healthy           BOOLEAN,
    health            JSONB,
    connected         BOOLEAN NOT NULL DEFAULT false,
    transport         TEXT NOT NULL DEFAULT '',
    remote_addr       TEXT NOT NULL DEFAULT '',
    last_token_id     UUID REFERENCES collector_tokens(id) ON DELETE SET NULL,
    first_seen_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (group_id, instance_uid)
);
CREATE INDEX IF NOT EXISTS idx_collectors_org ON collectors(organization_id);

-- collector_configs: configuration history, one row per change. The whole
-- configuration is encrypted (AES-256-GCM, key outside the database, bound
-- to its collector) so a database or backup reader sees no endpoints or
-- credentials. config_hash is an HMAC of the plaintext under a key derived
-- from the same cell key: it detects changes without letting a database
-- reader confirm a guessed password the way a bare hash would.
--
-- source says where a version came from. Stage 1 only stores what the
-- collector reports ('effective'); pushed and proposed versions are meant
-- to arrive here later with their own source and status.
CREATE TABLE IF NOT EXISTS collector_configs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    collector_id UUID NOT NULL REFERENCES collectors(id) ON DELETE CASCADE,
    source       TEXT NOT NULL DEFAULT 'effective' CHECK (source IN ('effective')),
    config_hash  TEXT NOT NULL,
    config_enc   TEXT NOT NULL,
    received_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_collector_configs_collector ON collector_configs(collector_id, received_at DESC);
