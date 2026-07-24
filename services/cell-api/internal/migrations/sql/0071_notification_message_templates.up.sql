-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Notification message templates (issue #5): message FORMAT gets the
-- same org→team ladder routing already has. One row per scope —
-- group_id NULL is the org-wide default set, a group row is that team's
-- override. Every field is Liquid and optional; empty string = inherit
-- (resolution is per FIELD, not per set). Deliberately separate from
-- notification profiles: routing and formatting change for different
-- reasons.

CREATE TABLE notification_message_templates (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    group_id        UUID REFERENCES groups(id) ON DELETE CASCADE,
    email_subject   TEXT NOT NULL DEFAULT '',
    email_body      TEXT NOT NULL DEFAULT '',
    slack_title     TEXT NOT NULL DEFAULT '',
    slack_body      TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One set per team, and exactly one org-default row (group_id NULL needs
-- a partial index — NULLs never collide in a plain unique constraint).
CREATE UNIQUE INDEX notification_message_templates_group_uq
    ON notification_message_templates (organization_id, group_id)
    WHERE group_id IS NOT NULL;
CREATE UNIQUE INDEX notification_message_templates_org_default_uq
    ON notification_message_templates (organization_id)
    WHERE group_id IS NULL;
