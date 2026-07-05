-- Attach an alert rule to a team (group). NULL = org-wide / unowned,
-- visible to everyone (preserves the pre-teams behaviour for every
-- existing rule). A team rule is visible/editable only by its members
-- + org admins — enforced in the cell-api handlers, not here.
--
-- ON DELETE SET NULL: deleting a team doesn't delete its alerts, it
-- just returns them to org-wide ownership. Losing the team link is far
-- less destructive than silently dropping monitoring rules.
ALTER TABLE alert_rules
    ADD COLUMN IF NOT EXISTS group_id UUID REFERENCES groups(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS alert_rules_group_idx
    ON alert_rules (organization_id, group_id)
    WHERE group_id IS NOT NULL;
