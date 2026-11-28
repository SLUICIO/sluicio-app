-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
ALTER TABLE collectors
    DROP COLUMN IF EXISTS plain_secrets_checked_at,
    DROP COLUMN IF EXISTS plain_secrets_at,
    DROP COLUMN IF EXISTS plain_secrets;
