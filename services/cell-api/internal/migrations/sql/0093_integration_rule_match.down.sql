ALTER TABLE integrations DROP CONSTRAINT IF EXISTS integrations_rule_match_check;
ALTER TABLE integrations DROP COLUMN IF EXISTS rule_match;
