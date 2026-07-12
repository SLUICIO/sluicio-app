-- Per-user UI preferences (column layouts, view defaults, …), keyed by
-- a namespaced string per surface (e.g. "integrations.columns"). Scoped
-- per org so a user active in several orgs keeps independent layouts
-- (metadata columns are org-specific anyway). Values are small JSON
-- blobs owned by the frontend; the API caps their size.
CREATE TABLE IF NOT EXISTS user_preferences (
    user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    organization_id UUID NOT NULL,
    key             TEXT NOT NULL,
    value           JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, organization_id, key)
);
