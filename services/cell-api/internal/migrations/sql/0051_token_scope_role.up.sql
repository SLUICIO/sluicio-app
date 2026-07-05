-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Per-token least-privilege (docs/api.md phase C). A token may be capped BELOW
-- its owner's role: scope_role '' = no cap (full owner role), else the token's
-- effective role is the more restrictive of (owner role, scope_role). Lets an
-- admin mint a read-only token, or an editor service account issue a viewer
-- token. Enforced in the auth middleware at the existing role gates.

ALTER TABLE api_tokens
    ADD COLUMN IF NOT EXISTS scope_role TEXT NOT NULL DEFAULT '';
