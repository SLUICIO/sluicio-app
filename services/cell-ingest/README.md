# cell-ingest

The cell's OTLP receiver. Accepts OTLP/gRPC and OTLP/HTTP from customer
OTel Collectors, authenticates each batch as a tenant, and writes:

- traces  → ClickHouse
- logs    → ClickHouse
- metrics → Prometheus (remote-write) or Mimir

Tenants in BYO mode never reach this service; their telemetry is queried
directly out of their own backends by `cell-api`.

License: FSL-1.1-Apache-2.0.
