-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
DROP TABLE IF EXISTS group_access_policies;

CREATE TABLE IF NOT EXISTS service_groups (
    org_id       UUID NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    service_name TEXT NOT NULL,
    group_id     UUID NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    assigned_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, service_name, group_id)
);
CREATE INDEX IF NOT EXISTS idx_service_groups_by_group   ON service_groups(group_id);
CREATE INDEX IF NOT EXISTS idx_service_groups_by_service ON service_groups(org_id, service_name);
