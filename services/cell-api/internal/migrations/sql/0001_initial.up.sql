-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- Initial cell-local schema. Preliminary.

-- Integrations: a named logical grouping of services.

CREATE TABLE integrations (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  UUID NOT NULL,
    slug             TEXT NOT NULL,
    name             TEXT NOT NULL,
    description      TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, slug)
);

-- Matcher rules: classify services / spans / metrics into integrations
-- by attribute pattern. Multiple matchers per integration are OR-ed.

CREATE TYPE matcher_operator AS ENUM ('equals', 'prefix', 'suffix', 'contains', 'regex');

CREATE TABLE integration_matchers (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id  UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    attribute       TEXT NOT NULL,                -- e.g. 'service.name'
    operator        matcher_operator NOT NULL,
    value           TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX integration_matchers_integration_idx ON integration_matchers (integration_id);

-- Alert rules.
-- rule_spec is the structured rule definition (JSON). It varies by
-- signal type and is validated by the rule engine.

CREATE TYPE alert_signal   AS ENUM ('metric', 'trace', 'log');
CREATE TYPE alert_severity AS ENUM ('info', 'warning', 'critical');

CREATE TABLE alert_rules (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id       UUID NOT NULL,
    integration_id        UUID REFERENCES integrations(id) ON DELETE SET NULL,
    name                  TEXT NOT NULL,
    description           TEXT,
    signal                alert_signal NOT NULL,
    rule_spec             JSONB NOT NULL,
    severity              alert_severity NOT NULL DEFAULT 'warning',
    evaluation_interval   INTERVAL NOT NULL DEFAULT INTERVAL '1 minute',
    enabled               BOOLEAN NOT NULL DEFAULT TRUE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX alert_rules_org_idx ON alert_rules (organization_id);

-- Notification channels.

CREATE TABLE notification_channels (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id  UUID NOT NULL,
    name             TEXT NOT NULL,
    kind             TEXT NOT NULL,    -- 'email', 'webhook', 'amqp', 'kafka'
    config           JSONB NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, name)
);

CREATE TABLE alert_rule_routes (
    alert_rule_id  UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    channel_id     UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    PRIMARY KEY (alert_rule_id, channel_id)
);

-- Alert instances.

CREATE TYPE alert_state AS ENUM ('pending', 'firing', 'resolved');

CREATE TABLE alert_instances (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_rule_id       UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    state               alert_state NOT NULL,
    started_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_evaluated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at            TIMESTAMPTZ,
    fingerprint         TEXT NOT NULL,
    labels              JSONB NOT NULL DEFAULT '{}'::jsonb,
    summary             TEXT,
    UNIQUE (alert_rule_id, fingerprint, started_at)
);
CREATE INDEX alert_instances_state_idx ON alert_instances (state);

-- Durable outbound dispatch queue. Workers claim jobs by updating
-- state to 'running' inside a transaction.

CREATE TYPE notification_job_state AS ENUM ('pending', 'running', 'succeeded', 'failed');

CREATE TABLE notification_jobs (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alert_instance_id  UUID NOT NULL REFERENCES alert_instances(id) ON DELETE CASCADE,
    channel_id         UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    state              notification_job_state NOT NULL DEFAULT 'pending',
    attempts           INT NOT NULL DEFAULT 0,
    last_error         TEXT,
    next_attempt_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX notification_jobs_due_idx ON notification_jobs (next_attempt_at)
    WHERE state = 'pending';

-- Append-only audit log of every meaningful action inside the cell.

CREATE TABLE audit_log (
    id               BIGSERIAL PRIMARY KEY,
    organization_id  UUID NOT NULL,
    actor_user_id    UUID,
    action           TEXT NOT NULL,
    resource_type    TEXT NOT NULL,
    resource_id      TEXT,
    payload          JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_org_time_idx ON audit_log (organization_id, occurred_at DESC);
