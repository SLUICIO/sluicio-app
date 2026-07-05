-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Cell operator (super-admin) flag. An operator is above the org-scoped
-- roles (admin/editor/viewer): they manage the org lifecycle and cell-wide
-- settings (SMTP, retention, license) that are shared across every org on
-- the cell. In single-org self-hosted the bootstrap admin is promoted to
-- operator on first boot, so nothing changes for that deployment; in a
-- multi-tenant cell it keeps one tenant's admin from touching shared
-- infrastructure settings.
--
-- The flag lives on users (not org_members) because it is org-independent:
-- an operator operates the cell, not a particular org.

ALTER TABLE users ADD COLUMN IF NOT EXISTS is_operator BOOLEAN NOT NULL DEFAULT false;

-- Fast lookup for "does any operator exist yet?" (the bootstrap check) and
-- for listing operators in the operator UI.
CREATE INDEX IF NOT EXISTS idx_users_is_operator ON users (is_operator) WHERE is_operator;
