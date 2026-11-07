# ClickHouse server configuration

`sluicio-logs.xml` is the configuration every Sluicio deployment gives its bundled ClickHouse, in `/etc/clickhouse-server/config.d/`.

ClickHouse keeps its own logs in tables in the `system` database. By default nothing deletes them, and at its default trace level `system.text_log` alone reaches gigabytes within weeks. They are not telemetry: the usage page and the storage limit do not count them, so they fill the disk unnoticed. This configuration logs at warning level, switches off the logs that are only for debugging ClickHouse itself, and keeps the query, part and error logs for 7 days.

The compose files carry the same XML inline (`SLUICIO_CLICKHOUSE_CONFIG`, written to `config.d/sluicio.xml` by the service's entrypoint) so that each stays a single file you can download; the Helm chart mounts `files/clickhouse-sluicio-logs.xml` from a ConfigMap. `TestEveryDeploymentConfiguresClickHouseLogs` keeps them identical to this file. Change them together.

Turning a log off does not delete its table. cell-ingest drops the switched-off log tables, and the copies ClickHouse sets aside when a log's settings change (`query_log_0`), once nothing has written to them for an hour, so a log that is still on is never dropped.
