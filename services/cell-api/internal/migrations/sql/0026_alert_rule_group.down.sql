DROP INDEX IF EXISTS alert_rules_group_idx;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS group_id;
