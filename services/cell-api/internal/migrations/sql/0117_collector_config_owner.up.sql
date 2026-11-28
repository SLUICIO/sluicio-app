-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Collector management: who owns a collector's configuration. Two writers
-- on one configuration overwrite each other, so each collector names one:
-- 'sluicio' (the default) or 'pipeline', the team's own deployment
-- pipeline. With the pipeline as owner, Sluicio sends nothing from its UI
-- (a pipeline that deploys through Sluicio still can, with an API token),
-- offers its suggestions as a diff to apply in the team's repository, and
-- does not flag drift, since the pipeline changing the configuration is
-- the pipeline doing its job.
ALTER TABLE collectors
    ADD COLUMN IF NOT EXISTS config_owner TEXT NOT NULL DEFAULT 'sluicio'
        CHECK (config_owner IN ('sluicio', 'pipeline')),
    ADD COLUMN IF NOT EXISTS config_owner_changed_by TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS config_owner_changed_at TIMESTAMPTZ;
