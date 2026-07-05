-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
-- Editable, human-owned metadata for a service. The service itself is
-- discovered from telemetry (name/namespace are not editable); this adds
-- the descriptive fields the Service detail page's Identity form owns.

CREATE TABLE IF NOT EXISTS service_metadata (
    organization_id  UUID NOT NULL,
    service_name     TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    owner            TEXT NOT NULL DEFAULT '',
    on_call          TEXT NOT NULL DEFAULT '',
    team             TEXT NOT NULL DEFAULT '',
    repository       TEXT NOT NULL DEFAULT '',
    runbook_url      TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, service_name)
);
