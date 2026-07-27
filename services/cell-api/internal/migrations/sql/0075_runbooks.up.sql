-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Runbooks on alert rules and system types (issue #8, WS4).
--
-- "When this fires: check X, typical cause Y, escalate to Z." The point
-- is machine-legible knowledge: the text rides along in notification and
-- event payloads and in MCP responses, so an agent woken by an alert
-- executes THE ORG'S playbook instead of inventing a plausible one. A
-- human paged at 3am benefits from exactly the same sentence.
--
-- Deliberately prose, not a URL. service_metadata.runbook_url already
-- covers "the wiki page for this service"; a link is useless to an agent
-- that cannot open it, and useless to a responder whose wiki is behind a
-- VPN they haven't connected to yet. The two coexist: the URL is where
-- to read more, the runbook is what to do now.
--
-- On system_types the runbook is the type-level default — "Kafka
-- consumer lag: check consumer group health first" — inherited as
-- context by the checks that type generates. Built-in types are
-- code-defined, so this column only carries custom and overridden ones.

ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS runbook TEXT NOT NULL DEFAULT '';
ALTER TABLE system_types ADD COLUMN IF NOT EXISTS runbook TEXT NOT NULL DEFAULT '';
