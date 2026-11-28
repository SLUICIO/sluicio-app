-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
ALTER TABLE collectors
    DROP COLUMN IF EXISTS drift_accepted_by_name,
    DROP COLUMN IF EXISTS drift_accepted_at,
    DROP COLUMN IF EXISTS drift_accepted_config_id,
    DROP COLUMN IF EXISTS drift_checked_at,
    DROP COLUMN IF EXISTS drift;
