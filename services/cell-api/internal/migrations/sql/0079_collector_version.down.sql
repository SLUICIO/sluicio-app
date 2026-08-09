ALTER TABLE orgs
  DROP COLUMN IF EXISTS collector_version,
  DROP COLUMN IF EXISTS collector_distribution;

ALTER TABLE services
  DROP COLUMN IF EXISTS collector_version,
  DROP COLUMN IF EXISTS collector_distribution;
