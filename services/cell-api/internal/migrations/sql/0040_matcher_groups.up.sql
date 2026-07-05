-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- match_group lets an integration's ATTRIBUTE matchers express OR, not
-- just AND. The integration's row predicate is disjunctive-normal-form:
-- matchers in the same match_group are AND-ed, and the groups are OR-ed —
--   (producer=abc) OR (consumer=dce)
--   (producer=x AND region=eu) OR (consumer=y)
-- service.name matchers (membership) ignore match_group; default 0 keeps
-- every existing matcher in a single AND-group (unchanged behaviour).

ALTER TABLE integration_matchers
    ADD COLUMN IF NOT EXISTS match_group SMALLINT NOT NULL DEFAULT 0;
