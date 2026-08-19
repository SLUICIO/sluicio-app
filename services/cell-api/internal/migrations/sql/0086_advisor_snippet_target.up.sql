-- Which collector a suggestion's snippet was written for (issue #16).
--
-- Collector configuration is not version-stable, so a snippet is only
-- meaningful alongside the version it targets. Storing it on the row
-- rather than deriving it at read time is deliberate: an ACCEPTED
-- suggestion's snippet is the audit trail of what was advised, and
-- re-deriving the target later would make that record describe a
-- decision nobody made.
--
-- snippet_unavailable carries the reason there is no snippet, for a
-- suggestion whose change cannot be expressed for the target. The
-- finding is still shown -- the cost it describes is real whether or
-- not we can write the fix -- but the YAML is withheld rather than
-- rendered with a caveat. A config that does not start is not improved
-- by a warning beside it: the reader weighs it at the moment they are
-- pasting into production, which is the worst possible place to put the
-- cost of our uncertainty.

ALTER TABLE advisor_suggestions
  ADD COLUMN IF NOT EXISTS snippet_target TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS snippet_unavailable TEXT NOT NULL DEFAULT '';
