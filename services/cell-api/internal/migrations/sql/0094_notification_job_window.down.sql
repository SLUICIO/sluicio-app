DROP INDEX IF EXISTS notification_jobs_failed_idx;
ALTER TABLE notification_jobs DROP COLUMN IF EXISTS for_state;
ALTER TABLE notification_jobs DROP COLUMN IF EXISTS expires_at;
