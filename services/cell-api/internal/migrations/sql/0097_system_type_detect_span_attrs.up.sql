-- Detect a system type from SPAN ATTRIBUTE keys, not only metric names.
--
-- Every type until now was recognised by the metrics a service emits,
-- which silently assumed every recognisable thing has metrics of its
-- own. Node-RED does not: its OpenTelemetry integration is a tracing
-- integration, and the only metrics it exports are HTTP request metrics
-- under generic semantic-convention names that every HTTP service in the
-- estate emits. There is no Node-RED metric name to match, and there is
-- no reason to expect one to appear.
--
-- What it does emit on every span is node_red.* attributes. The built-in
-- catalog gained a field for this in code; the column exists so an ORG
-- can express the same thing, and so the share format and config
-- transfer carry it rather than dropping it on the way out. A type
-- exported without its detection rule imports as a type that detects
-- nothing, which looks fine until nobody's service is ever recognised.
ALTER TABLE system_types
  ADD COLUMN IF NOT EXISTS detect_span_attrs jsonb NOT NULL DEFAULT '[]'::jsonb;
