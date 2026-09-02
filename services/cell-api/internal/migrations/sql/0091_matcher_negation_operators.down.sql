-- Postgres cannot remove a value from an enum. Dropping and recreating the
-- type would mean rewriting integration_matchers.operator and every
-- dependent object, to undo four additions that break nothing by being
-- present: a value nobody selects costs a row in pg_enum.
--
-- Rows using the new operators would fail to cast if the type were
-- rebuilt, so a real down migration would also have to decide what to do
-- with them - silently dropping a customer's matcher is worse than
-- leaving an unused enum label behind.
SELECT 1;
