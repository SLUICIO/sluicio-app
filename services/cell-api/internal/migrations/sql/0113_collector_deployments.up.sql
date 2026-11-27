-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Collector management over OpAMP, stage 2: sending a configuration to a
-- collector that accepts one (the supervisor type).
--
-- collector_deployments: each configuration sent, or waiting to be sent,
-- to one collector, and what the collector said about it. The body is
-- encrypted like a stored configuration (the cell's collector key, bound
-- to the collector and the hash). config_hash is the same HMAC as in
-- collector_configs; it doubles as the OpAMP config_hash the collector
-- echoes in its remote config status, so a status is matched to the
-- deployment it is about without a plain hash of the configuration ever
-- being stored.
--
-- status: proposed (waiting for approval, stage 3: the proposal points
-- here, so the configuration itself never sits in the proposals table in
-- the clear), pending (approved or sent directly, not delivered yet),
-- sent (delivered, no word back), applying, applied, failed, superseded
-- (a newer deployment replaced it before it applied). rolled_back_from
-- names the deployment a rollback restores.
CREATE TABLE IF NOT EXISTS collector_deployments (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    collector_id     UUID NOT NULL REFERENCES collectors(id) ON DELETE CASCADE,
    config_hash      TEXT NOT NULL,
    config_enc       TEXT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending'
                     CHECK (status IN ('proposed', 'pending', 'sent', 'applying', 'applied', 'failed', 'superseded')),
    error_message    TEXT NOT NULL DEFAULT '',
    message          TEXT NOT NULL DEFAULT '',
    created_by       UUID REFERENCES users(id) ON DELETE SET NULL,
    created_by_name  TEXT NOT NULL DEFAULT '',
    via              TEXT NOT NULL DEFAULT 'ui',
    proposal_id      UUID,
    rolled_back_from UUID REFERENCES collector_deployments(id) ON DELETE SET NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at          TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_collector_deployments_collector ON collector_deployments(collector_id, created_at DESC);

-- collector_team_settings: per team, whether a configuration change must
-- be approved before it is sent (Enterprise). No row means "send
-- directly", which is also what an unlicensed cell does.
CREATE TABLE IF NOT EXISTS collector_team_settings (
    group_id         UUID PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    require_approval BOOLEAN NOT NULL DEFAULT false,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
