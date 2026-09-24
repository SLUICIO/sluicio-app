-- Alert delivery survives a longer outage, and says what it gave up on
-- (issue #33).
--
-- expires_at: a delivery used to stop after five attempts, which with
-- the backoff is about sixteen minutes. That survives a receiver's lunch
-- break and not its evening. The ceiling is a time now, because "we try
-- for six hours" is something an operator can reason about and "five
-- attempts" is not. Existing rows get a window from now so a pending job
-- is not abandoned by the migration that widened the rule.
ALTER TABLE notification_jobs
    ADD COLUMN IF NOT EXISTS expires_at timestamptz NOT NULL DEFAULT now() + interval '6 hours';

-- for_state: which transition this delivery is FOR. The job carried no
-- memory of that, so the message was rendered from the instance's state
-- at delivery time: a firing notification delayed past the resolution
-- arrived describing the resolution, beside the resolved notification
-- that was queued for it.
ALTER TABLE notification_jobs
    ADD COLUMN IF NOT EXISTS for_state alert_state;

UPDATE notification_jobs j
   SET for_state = i.state
  FROM alert_instances i
 WHERE i.id = j.alert_instance_id AND j.for_state IS NULL;

-- Counting recent failures per channel, for the health line and the
-- count beside the channel. Partial: a cell's failures are a rounding
-- error against its deliveries, and this index should be too.
CREATE INDEX IF NOT EXISTS notification_jobs_failed_idx
    ON notification_jobs (channel_id, updated_at DESC)
    WHERE state = 'failed';
