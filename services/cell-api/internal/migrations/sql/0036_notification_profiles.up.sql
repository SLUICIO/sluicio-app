-- Notification profiles supersede the flat notification_routes added in
-- 0035 (which was never deployed). A profile bundles the *behaviour +
-- channels* for notifications and is owned by a team (group_id), or is the
-- org-wide default (group_id IS NULL). Each alert resolves to exactly one
-- profile, most-specific-first:
--   integration's assigned profile  ->  owning team's default profile
--                                    ->  the org-wide default profile.
--
-- grouping:        'per_check'        — one notification per firing check
--                  'per_integration'  — one digest per integration
-- renotify_minutes: 0 = notify once; else re-send while still active
--                   every N minutes (a reminder cadence).
DROP TABLE IF EXISTS notification_routes;

CREATE TABLE IF NOT EXISTS notification_profiles (
    id               UUID        PRIMARY KEY,
    organization_id  UUID        NOT NULL,
    group_id         UUID        REFERENCES groups(id) ON DELETE CASCADE, -- NULL = org-wide
    name             TEXT        NOT NULL,
    grouping         TEXT        NOT NULL DEFAULT 'per_check'
                                 CHECK (grouping IN ('per_check', 'per_integration')),
    renotify_minutes INTEGER     NOT NULL DEFAULT 0,
    is_default       BOOLEAN     NOT NULL DEFAULT FALSE, -- the default for its scope (team / org)
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_notification_profiles_group
    ON notification_profiles (organization_id, group_id);

-- A profile's delivery channels.
CREATE TABLE IF NOT EXISTS notification_profile_channels (
    profile_id UUID NOT NULL REFERENCES notification_profiles(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    PRIMARY KEY (profile_id, channel_id)
);

-- An integration can be assigned a specific profile (overrides the team /
-- org default). NULL = inherit.
ALTER TABLE integrations
    ADD COLUMN IF NOT EXISTS notification_profile_id UUID
    REFERENCES notification_profiles(id) ON DELETE SET NULL;
