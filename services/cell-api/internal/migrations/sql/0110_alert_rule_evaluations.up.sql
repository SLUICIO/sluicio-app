-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- When the engine last evaluated a metric check, and whether it found
-- data. alert_rule_readings only records a reading when there was one, so
-- a check that never found data (a scope that matches nothing, a renamed
-- source) had no trace of ever running and read as "ok": healthy, while
-- it could not fire. One row per rule, rewritten every evaluation.
CREATE TABLE IF NOT EXISTS alert_rule_evaluations (
    alert_rule_id UUID PRIMARY KEY REFERENCES alert_rules(id) ON DELETE CASCADE,
    evaluated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    had_data      BOOLEAN NOT NULL
);
