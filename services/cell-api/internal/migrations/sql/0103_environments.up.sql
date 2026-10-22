-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Environments on an integration (#40).

-- The environments an integration is expected to run in. Optional: an
-- integration that declares none is read from what its telemetry shows.
-- A declared environment with no traffic is a finding (quiet there),
-- where an undeclared one would simply not appear. "" is "(no
-- environment)".
ALTER TABLE integrations
    ADD COLUMN declared_environments TEXT[] NOT NULL DEFAULT '{}';

-- Which environments an integration-bound check evaluates:
--   each: every environment of the integration separately (the default),
--         one alert instance per environment;
--   only: just the listed ones, each separately.
ALTER TABLE alert_rules
    ADD COLUMN environment_mode TEXT NOT NULL DEFAULT 'each'
        CHECK (environment_mode IN ('each', 'only')),
    ADD COLUMN environments TEXT[] NOT NULL DEFAULT '{}';

-- Routing by environment: a profile can apply to some environments only
-- ("production only") or to all but some ("everything except
-- production"). A profile that does not apply to a firing's environment
-- is passed over for the next one down the ladder.
ALTER TABLE notification_profiles
    ADD COLUMN environment_mode TEXT NOT NULL DEFAULT 'any'
        CHECK (environment_mode IN ('any', 'only', 'except')),
    ADD COLUMN environments TEXT[] NOT NULL DEFAULT '{}';
