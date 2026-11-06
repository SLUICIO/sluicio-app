-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- Saved views get the visibility the save dialog always offered:
-- private (shared = FALSE: only owner_user_id sees it), team (group_id
-- set: that team's members see it) or org-wide (shared, no team). The
-- team column follows dashboards (0062), so a view and a dashboard
-- shared with a team behave alike, including ON DELETE SET NULL:
-- deleting a team makes its views org-wide rather than destroying them.
ALTER TABLE message_views ADD COLUMN IF NOT EXISTS group_id UUID REFERENCES groups(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS message_views_group_idx ON message_views(group_id) WHERE group_id IS NOT NULL;

-- Until now no owner was recorded and every view was shown to the whole
-- organization, whatever its shared flag said. A view with no owner
-- cannot be private to anyone, so it stays what it has been: org-wide.
UPDATE message_views SET shared = TRUE WHERE NOT shared AND owner_user_id IS NULL;
