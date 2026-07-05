-- last_notified_at tracks when a firing instance last produced a
-- notification, so the engine can re-notify on a profile's renotify
-- interval and coalesce per-integration delivery. NULL = never notified
-- (e.g. a per_integration alert awaiting its first digest tick).
ALTER TABLE alert_instances
    ADD COLUMN IF NOT EXISTS last_notified_at TIMESTAMPTZ;
