-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Which member of an integration each matcher group belongs to (#39).
--
-- A member is one entry on the integration's Services tab: a service, or
-- a pattern over service names, with the conditions that narrow it. A
-- member whose conditions are joined with "or" is stored as one group per
-- alternative, and nothing recorded that those groups belonged together.
-- With services combined as "one trace through every service" every
-- GROUP had to be satisfied, so the alternatives of one member became
-- separate requirements: "x = 1 or x = 2" was read as "x = 1 and x = 2".
-- Now one group per MEMBER must be.
--
-- The backfill changes no stored meaning:
--
--   rule_match = 'all': every group is its own member, which is exactly
--   how they were evaluated until now.
--
--   rule_match = 'any': the member does not affect what matches (the
--   integration is the union of all groups either way), so groups sharing
--   a service condition become one member - which is how the editor has
--   always shown them, as one row with its alternatives.

ALTER TABLE integration_matchers ADD COLUMN IF NOT EXISTS member INTEGER;

WITH service_groups AS (
    SELECT DISTINCT integration_id, match_group, operator::text AS op, value
    FROM integration_matchers
    WHERE attribute = 'service.name' OR attribute = ''
),
members AS (
    SELECT g.integration_id, g.match_group, min(o.match_group) AS member
    FROM service_groups g
    JOIN service_groups o
      ON o.integration_id = g.integration_id AND o.op = g.op AND o.value = g.value
    GROUP BY g.integration_id, g.match_group
)
UPDATE integration_matchers m
SET member = members.member
FROM members, integrations i
WHERE m.member IS NULL
  AND m.integration_id = members.integration_id
  AND m.match_group = members.match_group
  AND i.id = m.integration_id
  AND i.rule_match <> 'all';

UPDATE integration_matchers SET member = match_group WHERE member IS NULL;

ALTER TABLE integration_matchers ALTER COLUMN member SET NOT NULL;

-- A row written without a member is its own member: member = match_group,
-- which is today's meaning. Every path that writes matchers - the API,
-- cloning, config import, a bundle from an older version - keeps working
-- whether or not it knows about members yet.
CREATE OR REPLACE FUNCTION integration_matcher_member_default() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.member IS NULL THEN
        NEW.member := NEW.match_group;
    END IF;
    RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS integration_matchers_member_default ON integration_matchers;
CREATE TRIGGER integration_matchers_member_default BEFORE INSERT ON integration_matchers
    FOR EACH ROW EXECUTE FUNCTION integration_matcher_member_default();
