ALTER TABLE alert_rules
    DROP COLUMN IF EXISTS title_template,
    DROP COLUMN IF EXISTS body_template;
