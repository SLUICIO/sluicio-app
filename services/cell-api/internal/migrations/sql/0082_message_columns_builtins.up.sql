-- Built-in columns join the promoted ones in a single ordered list
-- (issue #23, follow-up).
--
-- v0.11.98 stored only promoted attributes there, and the message list
-- rendered its built-in columns unconditionally around them. Now the
-- list is the whole column set, so a row written under v0.11.98 would
-- read as "show ONLY these attributes" and silently lose the message
-- id, service, step and duration.
--
-- Rather than teach every reader to guess which shape it is holding,
-- upgrade the rows once: prepend the built-ins that were implicitly
-- there, tagged, in the order they were rendered. After this every row
-- is literal and no consumer needs the distinction.
--
-- Rows that are empty stay empty: empty means "defaults" both before
-- and after, and writing defaults into them would freeze today's set
-- into integrations nobody has configured.
--
-- The `kind` tag is what makes the two readable apart. Entries without
-- it are attributes, which is what the Go side already assumes, so this
-- migration only has to add tags rather than rewrite the attributes.

UPDATE integrations
SET message_columns = (
      '[{"kind":"builtin","key":"msg_id","label":"msg id"},'
    || '{"kind":"builtin","key":"service","label":"service"},'
    || '{"kind":"builtin","key":"step","label":"step"},'
    || '{"kind":"builtin","key":"duration","label":"duration"}]'
  )::jsonb || (
    SELECT COALESCE(jsonb_agg(elem || '{"kind":"attribute"}'::jsonb ORDER BY ord), '[]'::jsonb)
    FROM jsonb_array_elements(message_columns) WITH ORDINALITY AS t(elem, ord)
  )
WHERE jsonb_typeof(message_columns) = 'array'
  AND jsonb_array_length(message_columns) > 0
  -- Only rows that predate the tag. Re-running must not stack built-ins.
  AND NOT EXISTS (
    SELECT 1 FROM jsonb_array_elements(message_columns) AS e
    WHERE e ? 'kind'
  );
