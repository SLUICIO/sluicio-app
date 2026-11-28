-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Collector management, stage 3: a deployment made from an Advisor
-- suggestion remembers what it was for, so its effect can be measured
-- once the collector applied it (the volume of what it drops, before and
-- after).
--
-- advisor_class is the suggestion's class (T1 an unused metric, T2 a
-- service's unread logs) and advisor_target what the change drops
-- ({"metric": ...} or {"service": ..., "below": "WARN"}). Both are
-- copied rather than read through the suggestion: an open suggestion
-- whose finding stops being true is deleted by the next evaluation, and
-- dropped telemetry stops being a finding by design.
-- advisor_suggestion_id is kept for the link while the suggestion
-- exists; no foreign key for the same reason.
ALTER TABLE collector_deployments
    ADD COLUMN IF NOT EXISTS advisor_suggestion_id UUID,
    ADD COLUMN IF NOT EXISTS advisor_class TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS advisor_target JSONB;
