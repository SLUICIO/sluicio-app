-- Per-rule notification templates. Optional Go text/template strings
-- rendered at delivery time against the firing's context (rule_name,
-- metric, value, threshold, severity, state, summary, …). NULL/empty
-- = use the built-in auto-generated summary, so every existing rule
-- keeps its current wording with no change.
ALTER TABLE alert_rules
    ADD COLUMN IF NOT EXISTS title_template TEXT,
    ADD COLUMN IF NOT EXISTS body_template  TEXT;
