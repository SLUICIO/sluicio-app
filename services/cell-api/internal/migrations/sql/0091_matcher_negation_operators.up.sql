-- Negation and absence for integration matchers.
--
-- An integration could say "rpc.service = abc" but never "rpc.service is
-- absent" or "rpc.service is not abc", which is what you need to separate
-- traffic that carries an attribute from traffic that does not.
--
-- not_exists is NOT the same as `not_equals ''`: a ClickHouse Map returns
-- the empty string for a key it does not hold, so an absent attribute and
-- one explicitly set to "" are indistinguishable through any value
-- comparison. Only the existence check tells them apart.
--
-- ADD VALUE is not transactional in older Postgres and cannot be undone,
-- which is why the down migration leaves the enum alone (see the .down).
ALTER TYPE matcher_operator ADD VALUE IF NOT EXISTS 'not_equals';
ALTER TYPE matcher_operator ADD VALUE IF NOT EXISTS 'not_contains';
ALTER TYPE matcher_operator ADD VALUE IF NOT EXISTS 'exists';
ALTER TYPE matcher_operator ADD VALUE IF NOT EXISTS 'not_exists';
