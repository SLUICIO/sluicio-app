-- Which attributes an integration may be filtered by (issue #31).
--
-- The twin of message_columns from 0081. That column decides which
-- attributes appear as COLUMNS in the message list; this one decides
-- which may be used as FILTER FIELDS, and what they are called.
--
-- Where a service emits a hundred attributes, the filter picker is not a
-- feature but a haystack. An editor names the handful worth filtering on
-- for this integration, and can label customer.id as KundId so the
-- reader never meets the underlying attribute name.
--
-- DEFAULT '[]' means unrestricted, which is the behaviour every existing
-- integration has today. The restriction exists only where somebody
-- configured one, so nothing narrows on upgrade.
--
-- Shape: [{"key": "customer.id", "label": "KundId"}]
--
-- The label is display only. The stored filter, the API payload and the
-- audit trail keep the real key; a label in the data model would make
-- the field unsearchable the day somebody renames it.

ALTER TABLE integrations
  ADD COLUMN IF NOT EXISTS message_filters JSONB NOT NULL DEFAULT '[]'::jsonb;
