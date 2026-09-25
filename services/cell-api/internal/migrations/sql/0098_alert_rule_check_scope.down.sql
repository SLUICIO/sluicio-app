ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_check_scope_check;
ALTER TABLE alert_rules DROP COLUMN IF EXISTS check_scope;
