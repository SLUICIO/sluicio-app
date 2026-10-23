-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Metric and log sources of an integration (#43): telemetry that is
-- evidence about an integration without its origin being a member. A
-- broker serving many integrations is the typical origin: "Order sync"
-- owns the broker's metrics where the queue is orders or orders.dlq.
--
-- Never membership: a source is not in integration_services, the flow
-- graph, the message counts or the one-trace gate. It feeds the
-- integration's Metrics and Logs, its metric and log checks, and the
-- access a grant on the integration gives to those signals.
CREATE TABLE integration_sources (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    -- Which signal the source contributes.
    signal         TEXT NOT NULL CHECK (signal IN ('metrics', 'logs')),
    -- Where the telemetry comes from: a service by name, or a system by
    -- id (its member services, followed as they change).
    origin_kind    TEXT NOT NULL CHECK (origin_kind IN ('service', 'system')),
    origin         TEXT NOT NULL,
    -- Conditions on the rows' own attributes, AND-ed:
    -- [{"attribute":"rabbitmq.queue.name","operator":"equals","value":"orders"}].
    conditions     JSONB NOT NULL DEFAULT '[]',
    -- Metric sources only: which metrics, as globs (rabbitmq.queue.*).
    -- Empty is every metric of the origin that meets the conditions.
    metric_names   TEXT[] NOT NULL DEFAULT '{}',
    position       INT NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX integration_sources_integration ON integration_sources (integration_id, position);
