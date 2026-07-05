-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Reverse of 0017. Drops the auth tables in dependency order. The
-- "Default" org row in orgs gets removed too, which would orphan
-- every existing org_id reference elsewhere — only safe to run on a
-- database that has been reset to the pre-auth state.

DROP TABLE IF EXISTS api_tokens;
DROP TABLE IF EXISTS service_accounts;
DROP TABLE IF EXISTS org_members;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS orgs;
