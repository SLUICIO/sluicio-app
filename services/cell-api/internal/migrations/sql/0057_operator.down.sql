-- SPDX-License-Identifier: FSL-1.1-Apache-2.0

DROP INDEX IF EXISTS idx_users_is_operator;
ALTER TABLE users DROP COLUMN IF EXISTS is_operator;
