-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- A cap on how many integrations and systems the instance holds,
-- combined ("monitored entities"), set by the deployment
-- (SLUICIO_MONITORED_ENTITY_LIMIT). cell-api writes the current value
-- into deployment_limits at startup; NULL is no limit.
--
-- Enforced here rather than in the API because integrations and systems
-- are created along several paths - the API, cloning, config import,
-- marking a service as a system - and a cap checked in one of them is a
-- cap with a door beside it. The trigger is AFTER INSERT, so it counts
-- only rows actually created: an INSERT ... ON CONFLICT DO UPDATE that
-- reuses an existing system adds nothing and is not refused.

CREATE TABLE IF NOT EXISTS deployment_limits (
    id                     BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    monitored_entity_limit INTEGER CHECK (monitored_entity_limit IS NULL OR monitored_entity_limit >= 0),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO deployment_limits (id) VALUES (TRUE) ON CONFLICT DO NOTHING;

CREATE OR REPLACE FUNCTION enforce_monitored_entity_limit() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    lim   INTEGER;
    total BIGINT;
BEGIN
    SELECT monitored_entity_limit INTO lim FROM deployment_limits WHERE id;
    IF lim IS NULL THEN
        RETURN NULL;
    END IF;
    -- One creator at a time while a limit is set, so two simultaneous
    -- creates cannot both see room for one more. Held to the end of the
    -- transaction; each count below takes a fresh snapshot and so sees
    -- what the previous holder committed.
    PERFORM pg_advisory_xact_lock(hashtext('monitored_entity_limit'));
    SELECT (SELECT count(*) FROM integrations) + (SELECT count(*) FROM systems) INTO total;
    IF total > lim THEN
        RAISE EXCEPTION 'monitored entity limit reached: this instance allows % integrations and systems combined', lim
            USING ERRCODE = 'check_violation', CONSTRAINT = 'monitored_entity_limit';
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS integrations_entity_limit ON integrations;
CREATE TRIGGER integrations_entity_limit AFTER INSERT ON integrations
    FOR EACH ROW EXECUTE FUNCTION enforce_monitored_entity_limit();
DROP TRIGGER IF EXISTS systems_entity_limit ON systems;
CREATE TRIGGER systems_entity_limit AFTER INSERT ON systems
    FOR EACH ROW EXECUTE FUNCTION enforce_monitored_entity_limit();
