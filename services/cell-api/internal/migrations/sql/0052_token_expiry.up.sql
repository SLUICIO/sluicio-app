-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Optional token expiry (docs/api.md phase D). expires_at NULL = never expires;
-- otherwise the token stops resolving once now() passes it (enforced in
-- ResolveAPIToken alongside the revoked-at check). Rotation is revoke + reissue.

ALTER TABLE api_tokens
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;
