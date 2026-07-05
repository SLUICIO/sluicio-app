-- Per-service error acknowledgement ("clear errors"). When the
-- maintenance team has reviewed a service's failures, they clear them:
-- a watermark (acknowledged_until = now) plus an optional comment.
-- Health + error counts then ignore error traces at or before the
-- watermark, so the service reads healthy again until NEW failures
-- arrive. The failed traces themselves are untouched — this only
-- moves the "everything before here is handled" line.
--
-- One row per (org, service): re-clearing moves the watermark forward.
CREATE TABLE IF NOT EXISTS service_error_acks (
    organization_id    UUID        NOT NULL,
    service_name       TEXT        NOT NULL,
    acknowledged_until  TIMESTAMPTZ NOT NULL,
    comment            TEXT,
    acknowledged_by    UUID,        -- user who cleared; NULL for system/unauthenticated
    acknowledged_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, service_name)
);
