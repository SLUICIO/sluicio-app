-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Message-view scope: a saved view can be "scoped" to an entity (an
-- integration, a service, …) so that it appears in that entity's
-- scoped Messages tab as well as on the global Message Search page
-- with a "in <entity>" badge.
--
-- Scope is independent of role-based access. A NULL scope = global.
-- Today we model two scope kinds: integration_id (UUID) and
-- service_id (TEXT, the OTel service name). Either may be NULL.

ALTER TABLE message_views
    ADD COLUMN scope_integration_id UUID,
    ADD COLUMN scope_service_id     TEXT;

-- Index for the common lookup pattern on the scoped Messages tab:
-- "give me saved views for this integration".
CREATE INDEX message_views_scope_integration_idx
    ON message_views (organization_id, scope_integration_id)
    WHERE scope_integration_id IS NOT NULL;
