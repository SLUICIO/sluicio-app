-- Notification routing: where alerts (and unacknowledged errors) are
-- delivered when a rule has no explicit channel of its own. A route binds
-- a notification channel to a scope:
--   scope_kind = 'global'      -> the org-wide default (scope_id = '')
--   scope_kind = 'integration' -> scope_id = integration id
--   scope_kind = 'group'       -> scope_id = team (group) id
--
-- Resolution is most-specific-first: a firing alert uses its own channels
-- if set, else the integration's routes, else the owning team's routes,
-- else the global default. One generic table keeps all three levels in a
-- single store + query path.
CREATE TABLE IF NOT EXISTS notification_routes (
    organization_id UUID        NOT NULL,
    scope_kind      TEXT        NOT NULL CHECK (scope_kind IN ('global', 'integration', 'group')),
    scope_id        TEXT        NOT NULL DEFAULT '',   -- '' for global; integration/group id otherwise
    channel_id      UUID        NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, scope_kind, scope_id, channel_id)
);
CREATE INDEX IF NOT EXISTS idx_notification_routes_scope
    ON notification_routes (organization_id, scope_kind, scope_id);

-- Per-service "we've already notified about the current open errors"
-- watermark. The error notifier sends ONE notification when a service's
-- unacknowledged errors open, then records notified_at here so it doesn't
-- re-send every tick. Acknowledging (bumping service_error_acks) makes the
-- next new error eligible to notify again.
CREATE TABLE IF NOT EXISTS service_error_notifications (
    organization_id UUID        NOT NULL,
    service_name    TEXT        NOT NULL,
    notified_at     TIMESTAMPTZ NOT NULL,   -- when we last sent for this service
    last_error_at   TIMESTAMPTZ NOT NULL,   -- the error timestamp that triggered it
    PRIMARY KEY (organization_id, service_name)
);
