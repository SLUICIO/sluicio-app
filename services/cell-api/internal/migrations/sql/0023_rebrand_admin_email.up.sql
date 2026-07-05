-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Rename the seeded admin's email from the old project name to the
-- new one. Only renames when the email is still the default — if
-- someone already changed it in Settings → Account, we don't touch
-- their row.
--
-- Idempotent: the WHERE clause means re-running this migration is a
-- no-op once the rename has happened.

UPDATE users
SET    email      = 'admin@sluicio.local',
       updated_at = now()
WHERE  email      = 'admin@conduit.local';
