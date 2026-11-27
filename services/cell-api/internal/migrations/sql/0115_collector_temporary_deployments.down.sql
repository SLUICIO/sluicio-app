-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
DROP INDEX IF EXISTS idx_collector_deployments_expiry;
UPDATE collector_deployments SET status = 'superseded' WHERE status IN ('held', 'ended');
ALTER TABLE collector_deployments DROP CONSTRAINT IF EXISTS collector_deployments_status_check;
ALTER TABLE collector_deployments ADD CONSTRAINT collector_deployments_status_check
    CHECK (status IN ('proposed', 'pending', 'sent', 'applying', 'applied', 'failed', 'superseded'));
ALTER TABLE collector_deployments
    DROP COLUMN IF EXISTS ended_reason,
    DROP COLUMN IF EXISTS ended_at,
    DROP COLUMN IF EXISTS expires_at,
    DROP COLUMN IF EXISTS duration_seconds;
