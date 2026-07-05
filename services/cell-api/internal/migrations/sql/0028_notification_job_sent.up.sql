-- Capture exactly what was sent for the delivery-history view. The
-- rendered title/body are stamped onto the job when delivery succeeds,
-- so the log reflects what the recipient actually received even if the
-- rule's template changes later. NULL until a job succeeds (pending /
-- failed jobs have nothing sent yet).
ALTER TABLE notification_jobs
    ADD COLUMN IF NOT EXISTS sent_subject TEXT,
    ADD COLUMN IF NOT EXISTS sent_body    TEXT;

-- The history view lists newest-first across the org; updated_at is the
-- delivery / last-attempt time. Index it for the bounded recent query.
CREATE INDEX IF NOT EXISTS notification_jobs_updated_idx
    ON notification_jobs (updated_at DESC);
