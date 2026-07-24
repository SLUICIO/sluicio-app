-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Login-page announcements: an explicit opt-in flag. Only cell-wide
-- announcements (org_id IS NULL, operator-created) may carry it — the
-- login page is unauthenticated, so nothing org-scoped ever shows there.
-- Enforced in the API layer; the column default keeps every existing
-- announcement off the login page.

ALTER TABLE announcements
    ADD COLUMN show_on_login BOOLEAN NOT NULL DEFAULT false;
