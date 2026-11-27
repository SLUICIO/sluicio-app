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
ALTER TABLE collectors
    ADD COLUMN IF NOT EXISTS drift JSONB,
    ADD COLUMN IF NOT EXISTS drift_checked_at TIMESTAMPTZ;
