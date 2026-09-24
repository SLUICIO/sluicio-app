-- Postgres cannot remove a value from an enum, and rebuilding the type
-- would mean rewriting every row and every dependent object to undo one
-- addition that breaks nothing by being present.
SELECT 1;
