-- Whether a shared integration also grants its member services (#28).
--
-- Policies stopped lowering an integration grant to service names in
-- 0087. Shares were called out in the issue as needing the same
-- treatment and did not get it, so the leak stayed open on the other
-- path: sharing ONE integration on a shared runtime handed over every
-- sibling integration those services carry, telemetry included. A share
-- is the LESS privileged way to grant something - any editor can make
-- one, without touching policies - so it was the wider grant of the two.
--
-- DEFAULT TRUE for the same single reason 0087 had it: every share that
-- exists when this runs was made under the old meaning, and narrowing
-- them on upgrade would silently remove access somebody is relying on.
ALTER TABLE resource_shares
  ADD COLUMN IF NOT EXISTS grant_services BOOLEAN NOT NULL DEFAULT TRUE;

-- And immediately back to least privilege, in the same migration rather
-- than a later one. 0087 left the permissive default in place and it
-- took 0088 to close it, because an INSERT that omits the column gets
-- whatever the default says: the attachment path forgot, and handed out
-- service grants for months. Shares are created by one INSERT that does
-- not name this column, so the default IS the policy for every new row.
ALTER TABLE resource_shares ALTER COLUMN grant_services SET DEFAULT FALSE;
