-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
DROP INDEX IF EXISTS maintenance_windows_schedule_occurrence_idx;
ALTER TABLE maintenance_windows DROP COLUMN IF EXISTS schedule_id;
DROP TABLE IF EXISTS maintenance_schedules;
