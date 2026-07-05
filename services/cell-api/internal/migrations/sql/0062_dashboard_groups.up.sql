-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- RBAC v2 §5.2 A′ (decision 2026-07-04): dashboards become group-scopable.
-- NULL group_id = org-wide (visible to everyone, managed by org editors —
-- the historical behaviour, so existing rows are untouched). A group id =
-- the dashboard belongs to that team: visible to its members, managed by
-- its group-editors. ON DELETE SET NULL: deleting a team makes its
-- dashboards org-wide rather than destroying them.
ALTER TABLE dashboards ADD COLUMN IF NOT EXISTS group_id UUID REFERENCES groups(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS dashboards_group_idx ON dashboards(group_id) WHERE group_id IS NOT NULL;
