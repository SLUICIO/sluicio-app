-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Collector management, stage 3: a deployment made from an Advisor
-- suggestion remembers what it was for, so its effect can be measured
-- once the collector applied it (the metric's volume before and after).
--
-- advisor_metric is the metric the change drops, copied rather than
-- read through the suggestion: an open suggestion whose finding stops
-- being true is deleted by the next evaluation, and a dropped metric
-- stops being a finding by design. advisor_suggestion_id is kept for the
-- link while the suggestion exists; no foreign key for the same reason.
ALTER TABLE collector_deployments
    ADD COLUMN IF NOT EXISTS advisor_suggestion_id UUID,
    ADD COLUMN IF NOT EXISTS advisor_metric TEXT NOT NULL DEFAULT '';
