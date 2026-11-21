-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- The email domains an SSO provider speaks for. On a cell with several
-- organizations every org admin can configure an identity provider, and
-- an IdP can assert any email address it likes. Without a boundary, one
-- org's IdP could sign in as another org's user by asserting their email.
-- A provider may only link to, or create, accounts in the domains listed
-- here, and the login page routes an email to the provider that holds its
-- domain, so the page never has to list organizations.
--
-- domain is the primary key: one provider per domain on the whole cell.
-- Domains are assigned by a cell operator, because the assignment decides
-- whose sign-in page a user is sent to.
CREATE TABLE IF NOT EXISTS auth_provider_domains (
    domain      TEXT PRIMARY KEY CHECK (domain = lower(domain) AND domain <> ''),
    provider_id UUID NOT NULL REFERENCES auth_providers(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_auth_provider_domains_provider ON auth_provider_domains(provider_id);
