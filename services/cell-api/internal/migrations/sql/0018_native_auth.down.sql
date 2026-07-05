-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Reverse of 0018. Drops the native-auth additions and restores
-- the keycloak_sub column on users. The data is gone — only safe
-- on a database you're rolling back deliberately.

DROP TABLE IF EXISTS auth_providers;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS oidc_subjects;

ALTER TABLE users
    DROP COLUMN IF EXISTS password_hash,
    DROP COLUMN IF EXISTS must_reset_password,
    ADD COLUMN IF NOT EXISTS keycloak_sub UUID;
CREATE INDEX IF NOT EXISTS idx_users_keycloak_sub ON users(keycloak_sub);
