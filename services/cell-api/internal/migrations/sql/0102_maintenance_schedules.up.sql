-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Recurring maintenance windows (#9): "every Sunday 02:00-04:00".
--
-- A schedule is the series. Each occurrence becomes an ordinary
-- maintenance_windows row, created by cell-api shortly before (or, for a
-- series created mid-occurrence, during) it, linked back by schedule_id.
-- Suppression, announcements and "end early" therefore work on an
-- occurrence exactly as on a one-shot window, with nothing new in the
-- alert engine.
CREATE TABLE maintenance_schedules (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    -- Same shape as maintenance_windows.scope. System members are
    -- expanded per occurrence, not once for the series.
    scope       JSONB NOT NULL,
    -- {"frequency":"weekly","weekdays":[0],"start_time":"02:00",
    --  "duration_minutes":120,"timezone":"Europe/Stockholm","ends_on":"…"}
    -- or "monthly" with month_week (1-4, -1 last) and month_weekday.
    recurrence  JSONB NOT NULL,
    -- Create a banner for each occurrence, as "announce" does for a window.
    announce    BOOLEAN NOT NULL DEFAULT false,
    -- Occurrence starts someone cancelled ("not this Sunday").
    skipped_starts TIMESTAMPTZ[] NOT NULL DEFAULT '{}',
    created_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX maintenance_schedules_org_idx ON maintenance_schedules (org_id);

-- An occurrence keeps its row (and the suppression history on it) when
-- its series is deleted.
ALTER TABLE maintenance_windows
    ADD COLUMN schedule_id UUID REFERENCES maintenance_schedules(id) ON DELETE SET NULL;

-- One row per occurrence, so creating it twice is a no-op.
CREATE UNIQUE INDEX maintenance_windows_schedule_occurrence_idx
    ON maintenance_windows (schedule_id, starts_at) WHERE schedule_id IS NOT NULL;
