-- Which span attributes an integration promotes to columns in its
-- message list (issue #23).
--
-- Stored as an ordered JSONB array of {key, label} rather than a table
-- because order IS the data here — it is the left-to-right order of the
-- columns — and a row order in Postgres is not a thing you get for
-- free. An array also makes the write atomic: reordering, relabelling
-- and removing are one UPDATE, not a diff against existing rows.
--
-- The label is stored, not derived. Deriving it from the key would be a
-- decent default and a bad rule: "Documents exported" and
-- "documents.exported" carry the same information to a machine and very
-- different information to the person reading a report. The UI
-- pre-fills a humanised key and lets it be overwritten; what the user
-- settled on is what belongs in the database.
--
-- Empty array = the previous behaviour (the first few attributes of the
-- matched span), so nothing changes for an integration nobody has
-- configured.

ALTER TABLE integrations
  ADD COLUMN IF NOT EXISTS message_columns JSONB NOT NULL DEFAULT '[]'::jsonb;
