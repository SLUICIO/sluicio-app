DROP INDEX IF EXISTS dashboards_group_idx;
ALTER TABLE dashboards DROP COLUMN IF EXISTS group_id;
