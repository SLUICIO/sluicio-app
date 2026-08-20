-- Flip the default for grant_services to least privilege (#28).
--
-- 0087 added the column with DEFAULT TRUE for one reason: to preserve
-- the meaning of policies that already existed, which did grant their
-- member services. That backfill is done.
--
-- Leaving the default TRUE would quietly re-open the hole from the other
-- side. Any INSERT that omits the column gets a service grant, and the
-- integration-to-group attachment path did exactly that, so "attach this
-- integration to this team" would still have handed over every sibling
-- integration on the same runtime.
--
-- The insert now names the column explicitly, and the default is FALSE
-- so the next writer to forget is safe rather than sorry.

ALTER TABLE group_access_policies ALTER COLUMN grant_services SET DEFAULT FALSE;
