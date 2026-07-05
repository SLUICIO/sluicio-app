-- resolve_mode controls what happens when a health check's condition
-- clears: 'auto' resolves the alert automatically (self-recovering);
-- 'manual' keeps it firing until a human acknowledges it. Previously this
-- was hardwired by signal (metric auto-resolved; log + failed-trace were
-- sticky). Make it a per-check choice, defaulting new rows to 'auto' but
-- preserving the old behaviour for existing rules.
ALTER TABLE alert_rules
    ADD COLUMN IF NOT EXISTS resolve_mode TEXT NOT NULL DEFAULT 'auto';

-- Existing log + failed-trace checks were sticky (require acknowledgement).
UPDATE alert_rules SET resolve_mode = 'manual' WHERE signal IN ('log', 'trace');
-- (resolve_mode lives on alert_rules; new rows default 'auto', the API sets
--  the right default per signal at create time.)
