ALTER TABLE advisor_suggestions
  DROP COLUMN IF EXISTS snippet_target,
  DROP COLUMN IF EXISTS snippet_unavailable;
