-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Announcements (persistent user-facing banners) + maintenance windows
-- (bounded alert-delivery suppression). See
-- docs/maintenance-and-announcements-design.md.

CREATE TABLE announcements (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- NULL org_id = cell-wide (operator-created), shown to every org.
    org_id      UUID REFERENCES orgs(id) ON DELETE CASCADE,
    message     TEXT NOT NULL,
    severity    TEXT NOT NULL DEFAULT 'info'
                CHECK (severity IN ('info', 'warning', 'critical')),
    starts_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    ends_at     TIMESTAMPTZ,
    dismissible BOOLEAN NOT NULL DEFAULT true,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX announcements_org_idx ON announcements (org_id);

-- Per-user dismissal state — server-side so it survives devices and
-- cache clears.
CREATE TABLE announcement_dismissals (
    announcement_id UUID NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    dismissed_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (announcement_id, user_id)
);

-- Maintenance windows: while active, alert *delivery* for the covered
-- scope is suppressed (evaluation continues; instances are recorded and
-- flagged). ends_at is NOT NULL by design — no forever-silences.
CREATE TABLE maintenance_windows (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    starts_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    ends_at     TIMESTAMPTZ NOT NULL,
    -- {"kind":"all_org"} | {"kind":"entities","integration_ids":[…],
    -- "system_ids":[…],"service_names":[…]} | {"kind":"group","group_id":…}
    scope       JSONB NOT NULL,
    -- Linked auto-announcement (created when announce was requested;
    -- deleted when the window ends early).
    announcement_id UUID REFERENCES announcements(id) ON DELETE SET NULL,
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (ends_at > starts_at)
);
CREATE INDEX maintenance_windows_org_active_idx
    ON maintenance_windows (org_id, ends_at);

-- Instances that fired while covered carry the window that muted them;
-- their resolve notification is suppressed too (never announced ⇒ never
-- "resolved"). Instances that fired *before* a window keep NULL and
-- resolve loudly as usual.
ALTER TABLE alert_instances
    ADD COLUMN suppressed_by UUID REFERENCES maintenance_windows(id) ON DELETE SET NULL;
