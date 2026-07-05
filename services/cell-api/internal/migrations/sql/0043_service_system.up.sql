-- Mark a service as a monitored "system" (e.g. RabbitMQ, SQL Server). A system
-- is just a service flagged here: its health rolls up from its own health
-- checks, it gets a System badge in the services list, and it's surfaced in the
-- Systems view. system_kind (rabbitmq/sqlserver/…) drives the icon + which
-- built-in monitoring template applies. Both default off/empty — set by a user.
ALTER TABLE services
    ADD COLUMN IF NOT EXISTS is_system   BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS system_kind TEXT    NOT NULL DEFAULT '';
