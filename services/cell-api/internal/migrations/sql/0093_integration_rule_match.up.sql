-- How an integration's rules combine.
--
-- Until now they were always a union: a span belonged to the integration
-- if it satisfied ANY rule. That is right for "these services are one
-- integration" and wrong for "this flow is the one that goes through all
-- of these", which is the shape you get as soon as a rule carries an
-- attribute condition: a trace matching the b2b-gateway rule was pulled
-- in whatever the order-validator span said, because some other span had
-- satisfied some other rule.
--
-- 'all' asks the question of the TRACE instead: every rule has to be
-- satisfied by some span of the same trace. 'any' is the default, so
-- every integration that exists keeps matching exactly what it matched.
ALTER TABLE integrations
    ADD COLUMN IF NOT EXISTS rule_match text NOT NULL DEFAULT 'any';

ALTER TABLE integrations
    DROP CONSTRAINT IF EXISTS integrations_rule_match_check;
ALTER TABLE integrations
    ADD CONSTRAINT integrations_rule_match_check CHECK (rule_match IN ('any', 'all'));
