-- What a service-bound check is ABOUT: the process, or one flow of it.
--
-- An integration that selects a slice of a shared service reads its
-- member's checks only when they describe the whole process (a dead
-- scheduler breaks every DAG), and not when they describe one flow (one
-- DAG failing must not mark its siblings). That distinction shipped
-- inferred: a metric check with no split and no attribute filter was
-- read as process-level, everything else as flow-level.
--
-- The inference is right about the common cases and wrong in the
-- dangerous direction about one. An attribute filter can select a FLOW
-- (dag_id = gl_posting) or merely a SERIES OF THE SAME PROCESS
-- (instance = scheduler-1, pool_name = default). Read as flow-level, a
-- heartbeat narrowed to one instance stops reaching any slice of that
-- service - so the check still fires, and every integration that
-- depended on hearing about it goes on reading "ok". It fails quiet.
--
-- The filter KEY is what tells those apart, and no list of keys can be
-- right for every estate. So the answer is not a better guess: it is to
-- let the check say. 'process' and 'flow' are declarations that win;
-- NULL keeps the inference, which is what every rule that exists today
-- and every hand-written rule still gets.
ALTER TABLE alert_rules
  ADD COLUMN IF NOT EXISTS check_scope text;

ALTER TABLE alert_rules
  DROP CONSTRAINT IF EXISTS alert_rules_check_scope_check;
ALTER TABLE alert_rules
  ADD CONSTRAINT alert_rules_check_scope_check
  CHECK (check_scope IS NULL OR check_scope IN ('process', 'flow'));
