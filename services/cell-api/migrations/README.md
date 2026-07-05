# Cell-local migrations

Postgres schema migrations for the cell's local metadata database
(integration definitions, alert rules, notification channels, alert
instances, dispatch queue, audit log).

Telemetry itself lives in ClickHouse and Prometheus, not here. This
database holds only the small relational metadata that drives the cell.

Files follow the `NNNN_short_name.{up,down}.sql` convention. The schema
is **preliminary** and will evolve.
