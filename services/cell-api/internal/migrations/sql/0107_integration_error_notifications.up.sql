-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Whether an integration's people are notified when its services record
-- errors nobody has acknowledged. Off by default: the errors stay on the
-- Errors page and keep counting there, and a notification is sent only
-- for integrations that ask for one. Before this every new batch of
-- unacknowledged errors paged the integration's profile, or the org
-- default for a service in no integration, with no way to stop it.
ALTER TABLE integrations ADD COLUMN IF NOT EXISTS notify_unacknowledged_errors BOOLEAN NOT NULL DEFAULT FALSE;
