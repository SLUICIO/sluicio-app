-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Payload links: the span attributes an organization turns into links
-- to a message's payload (the file picked up, the invoice posted), shown
-- as columns in every message list and on the spans of a trace.
--
-- One ordered JSONB array per organization, [{attribute, label,
-- base_url}], because the order is the left-to-right order of the
-- columns and a save replaces the whole list. No row means the defaults
-- (io.payload.url, no base URL); a row with an empty array means an admin
-- removed every attribute.

CREATE TABLE IF NOT EXISTS org_payload_links (
    organization_id UUID PRIMARY KEY REFERENCES orgs(id) ON DELETE CASCADE,
    attributes      JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
