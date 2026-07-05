-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
--
-- User-defined metadata fields that decorate integrations and/or services.
-- Modelled like a small schema editor: a field is defined once for the
-- organisation (key, label, type, applies-to), and any number of values
-- can then be attached to integrations / services. Built-in service
-- fields (description, owner, on-call, …) live in the separate
-- service_metadata table and are unaffected.

CREATE TABLE IF NOT EXISTS metadata_fields (
    id                     UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id        UUID NOT NULL,
    -- Machine-friendly identifier, unique within the org. Used as the
    -- map key on the integration / service detail response payloads.
    key                    TEXT NOT NULL,
    -- Human-readable label shown in forms and value panels.
    label                  TEXT NOT NULL,
    -- Value shape. The frontend renders the matching input variant and
    -- the backend stores everything as TEXT (parsed on read).
    type                   TEXT NOT NULL CHECK (type IN ('text', 'boolean', 'number', 'select')),
    -- For type='select', the list of allowed values as a JSON array of
    -- strings. NULL / empty for other types.
    options                JSONB,
    description            TEXT NOT NULL DEFAULT '',
    -- Either / both of the scopes the field applies to.
    applies_to_integration BOOLEAN NOT NULL DEFAULT false,
    applies_to_service     BOOLEAN NOT NULL DEFAULT false,
    -- Required fields must have a non-empty value on save.
    required               BOOLEAN NOT NULL DEFAULT false,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (organization_id, key),
    -- A field with no scope is meaningless.
    CHECK (applies_to_integration OR applies_to_service)
);
CREATE INDEX IF NOT EXISTS idx_metadata_fields_org ON metadata_fields(organization_id);

-- Values attached to an integration.
CREATE TABLE IF NOT EXISTS integration_metadata (
    integration_id UUID NOT NULL REFERENCES integrations(id) ON DELETE CASCADE,
    field_id       UUID NOT NULL REFERENCES metadata_fields(id) ON DELETE CASCADE,
    value          TEXT NOT NULL,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (integration_id, field_id)
);
CREATE INDEX IF NOT EXISTS idx_integration_metadata_field ON integration_metadata(field_id);

-- Values attached to a service. Services have no surrogate id (the name
-- is the identity), so the key is (org, service_name, field).
CREATE TABLE IF NOT EXISTS service_metadata_extras (
    organization_id UUID NOT NULL,
    service_name    TEXT NOT NULL,
    field_id        UUID NOT NULL REFERENCES metadata_fields(id) ON DELETE CASCADE,
    value           TEXT NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, service_name, field_id)
);
CREATE INDEX IF NOT EXISTS idx_service_metadata_extras_field ON service_metadata_extras(field_id);
CREATE INDEX IF NOT EXISTS idx_service_metadata_extras_svc ON service_metadata_extras(organization_id, service_name);
