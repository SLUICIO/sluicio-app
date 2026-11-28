-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Collector management: drift. Whether a collector still runs what was
-- last rolled out to it, checked when it reports its configuration or
-- applies a deployment, and kept here so the collector list can show it
-- without opening every stored configuration.
--
-- drift: {"deployment_id": ..., "paths": [{"path": ..., "kind": "missing" | "changed"}]}.
-- It names paths only, never values, which may be secrets. An empty
-- paths list means in sync; NULL means not checked or not applicable (a
-- read-only collector, no deployment applied yet, or no configuration as
-- written to compare with).
--
-- drift_accepted_*: someone accepted the collector's configuration as the
-- new baseline (changes made on the host that are meant to stay); drift is
-- then measured from that version until the next rollout from Sluicio.
-- No foreign key: the version is deleted with the collector anyway, and a
-- key back from collectors to its own configurations would make that
-- delete a cycle.
ALTER TABLE collectors
    ADD COLUMN IF NOT EXISTS drift JSONB,
    ADD COLUMN IF NOT EXISTS drift_checked_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS drift_accepted_config_id UUID,
    ADD COLUMN IF NOT EXISTS drift_accepted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS drift_accepted_by_name TEXT NOT NULL DEFAULT '';
