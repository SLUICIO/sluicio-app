-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
ALTER TABLE alert_instances DROP COLUMN IF EXISTS suppressed_by;
DROP TABLE IF EXISTS maintenance_windows;
DROP TABLE IF EXISTS announcement_dismissals;
DROP TABLE IF EXISTS announcements;
