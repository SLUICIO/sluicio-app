-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- Deleting an integration deletes the health checks bound to it.
--
-- integration_id has been ON DELETE SET NULL since 0001. A check bound to
-- an integration evaluates over that integration's slice; with the
-- binding nulled and no service or system left, the same row evaluates
-- over EVERY service in the org. "Ready messages > 1000" on one queue
-- became "ready messages > 1000" on every queue of every broker, still
-- enabled, still routed, and free to fire on queues nobody meant it for.
--
-- 0077 already chose CASCADE for system_id for exactly this reason and
-- left integration_id alone for backwards compatibility. There is nothing
-- worth keeping compatible: no one wants the widened rule.
--
-- Rules that a past delete already widened cannot be told apart from
-- rules that were written global on purpose, so they are left as they
-- are. This only stops new ones from appearing.
--
-- The constraint was declared inline in 0001, so its name is whatever
-- Postgres generated. Find it rather than assume it.
DO $$
DECLARE
    fk text;
BEGIN
    FOR fk IN
        SELECT c.conname
        FROM pg_constraint c
        JOIN pg_attribute a
          ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
        WHERE c.contype = 'f'
          AND c.conrelid = 'alert_rules'::regclass
          AND c.confrelid = 'integrations'::regclass
          AND a.attname = 'integration_id'
    LOOP
        EXECUTE format('ALTER TABLE alert_rules DROP CONSTRAINT %I', fk);
    END LOOP;
END $$;

ALTER TABLE alert_rules
    ADD CONSTRAINT alert_rules_integration_id_fkey
    FOREIGN KEY (integration_id) REFERENCES integrations(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS alert_rules_integration_idx
    ON alert_rules (organization_id, integration_id)
    WHERE integration_id IS NOT NULL;
