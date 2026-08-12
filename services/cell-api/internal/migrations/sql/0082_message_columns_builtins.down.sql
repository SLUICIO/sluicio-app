-- Drop the built-in entries and the kind tags, leaving the attribute
-- list v0.11.98 expected. A column the user removed on purpose comes
-- back, because the older shape has no way to say "not this one".

UPDATE integrations
SET message_columns = (
    SELECT COALESCE(jsonb_agg((elem - 'kind') ORDER BY ord), '[]'::jsonb)
    FROM jsonb_array_elements(message_columns) WITH ORDINALITY AS t(elem, ord)
    WHERE COALESCE(elem->>'kind', 'attribute') <> 'builtin'
  )
WHERE jsonb_typeof(message_columns) = 'array'
  AND jsonb_array_length(message_columns) > 0;
