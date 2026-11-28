-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Collector management: time-limited deployments. A configuration can be
-- sent for a limited time (debug logging during an incident, trimming
-- lifted to see what arrives); when the time is up, the server sends the
-- collector its base again.
--
-- duration_seconds: set on a temporary deployment; the time starts when
-- the collector reports it applied, which is when expires_at is set.
-- ended_at and ended_reason (expired, ended) record how a temporary
-- deployment stopped; its status is then 'ended'.
--
-- New statuses: 'held' is a permanent change sent while a temporary one
-- is active; it is the base the collector returns to and is delivered
-- when the temporary one ends. 'ended' is a temporary deployment that
-- has been reverted.
ALTER TABLE collector_deployments
    ADD COLUMN IF NOT EXISTS duration_seconds INTEGER,
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS ended_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS ended_reason TEXT NOT NULL DEFAULT '';

ALTER TABLE collector_deployments DROP CONSTRAINT IF EXISTS collector_deployments_status_check;
ALTER TABLE collector_deployments ADD CONSTRAINT collector_deployments_status_check
    CHECK (status IN ('proposed', 'pending', 'held', 'sent', 'applying', 'applied', 'failed', 'superseded', 'ended'));

-- The expiry sweep looks for applied temporary deployments past their
-- time, across every collector, every few seconds.
CREATE INDEX IF NOT EXISTS idx_collector_deployments_expiry
    ON collector_deployments(expires_at) WHERE status = 'applied' AND expires_at IS NOT NULL;
