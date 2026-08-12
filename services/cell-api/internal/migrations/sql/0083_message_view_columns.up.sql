-- A saved view carries its own column set (issue #23, follow-up).
--
-- Columns landed on the integration, which answers "how does this
-- integration read by default". It does not answer the other question a
-- saved view exists for: two people watching the same integration for
-- different reasons want different columns, and a view is exactly the
-- place that difference is supposed to live. A view already stores
-- WHICH messages you care about; which FACTS about them you care about
-- belongs beside it.
--
-- NULL, not '[]'. The two mean different things here and the
-- distinction is the whole feature:
--
--   NULL  — this view has no opinion; fall back to the integration's
--           columns, so a view keeps working when the integration's
--           default is improved
--   '[]'  — this view deliberately shows no columns beyond the fixed
--           chrome, which is a choice somebody made and must survive
--
-- A DEFAULT of '[]' would erase that difference for every view that
-- already exists, silently converting "no opinion" into "show nothing".

ALTER TABLE message_views
  ADD COLUMN IF NOT EXISTS message_columns JSONB;

COMMENT ON COLUMN message_views.message_columns IS
  'Ordered column set for this view; NULL means inherit the integration''s.';
