-- Which collector a generated snippet targets (issue #16).
--
-- Two levels, deliberately. The ORG default is what almost every
-- customer needs. The per-SERVICE override is what makes the feature
-- honest: a snippet always targets one service's pipeline, so a customer
-- running a newer collector on one host than another would otherwise get
-- correct YAML for some services and YAML that will not start for
-- others, with no way to say so.
--
-- Both are nullable and both default to unset rather than to a value.
-- Unset means "use the newest version this Sluicio build knows", which
-- is a decision that belongs in code where it can move with each
-- release, not frozen into a column default that would silently age.
--
-- Distribution is stored alongside the version because it matters just
-- as much: a component present in contrib may be absent from core, so a
-- version number alone answers only half the question.

ALTER TABLE orgs
  ADD COLUMN IF NOT EXISTS collector_version TEXT,
  ADD COLUMN IF NOT EXISTS collector_distribution TEXT;

ALTER TABLE services
  ADD COLUMN IF NOT EXISTS collector_version TEXT,
  ADD COLUMN IF NOT EXISTS collector_distribution TEXT;
