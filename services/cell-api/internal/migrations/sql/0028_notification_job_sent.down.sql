DROP INDEX IF EXISTS notification_jobs_updated_idx;
ALTER TABLE notification_jobs
    DROP COLUMN IF EXISTS sent_subject,
    DROP COLUMN IF EXISTS sent_body;
