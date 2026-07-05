-- Per-rule notification content: which enrichment blocks (service, integration,
-- their metadata, the failing check) the alert's email/webhook includes, plus
-- an optional inline Liquid email template. NULL = no enrichment + the org
-- default email template (back-compat with rules created before this feature).
ALTER TABLE alert_rules ADD COLUMN IF NOT EXISTS notification_config JSONB;
