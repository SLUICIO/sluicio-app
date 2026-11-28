-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
ALTER TABLE collectors
    DROP COLUMN IF EXISTS config_owner_changed_at,
    DROP COLUMN IF EXISTS config_owner_changed_by,
    DROP COLUMN IF EXISTS config_owner;
