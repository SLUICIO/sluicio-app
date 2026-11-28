-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Collector management: where a collector's configuration as written holds
-- a secret in plain text instead of an ${env:...} reference, so its team
-- is told and can move it into a variable. Paths only, never values.
-- plain_secrets_at is when the configuration first held one (cleared when
-- it no longer does); plain_secrets_checked_at is NULL for a collector
-- whose configuration was stored before the check existed, so it is
-- checked on its next report.
ALTER TABLE collectors
    ADD COLUMN IF NOT EXISTS plain_secrets JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS plain_secrets_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS plain_secrets_checked_at TIMESTAMPTZ;
