-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Dashboards per environment and by rule (issue #40 follow-up).

-- The environments a dashboard reads: empty is all of them (as before),
-- one narrows every card to it, several give each card a row per
-- environment. "" is "(no environment)".
ALTER TABLE dashboards
    ADD COLUMN environments TEXT[] NOT NULL DEFAULT '{}',
    -- Narrows "show every integration" to the ones matching a rule:
    -- {"kind":"metadata","key":"country","value":"Sweden"} or
    -- {"kind":"tag","value":"payments"}. NULL = every integration.
    ADD COLUMN include_rule JSONB;
