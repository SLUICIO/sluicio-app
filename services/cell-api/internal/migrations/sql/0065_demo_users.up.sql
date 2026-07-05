-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Shared demo accounts: is_demo blocks the self-service account surface
-- (profile edit, password change, MFA enrollment, token minting) so a
-- visitor on a shared demo login cannot sabotage the identity for
-- everyone else. Orthogonal to RBAC: roles/groups still decide what the
-- account sees and touches in the product.
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_demo BOOLEAN NOT NULL DEFAULT false;
