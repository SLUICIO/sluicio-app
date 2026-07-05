# cell-api

The cell's HTTP API. Serves the frontend, runs queries against the cell's
telemetry backends, and owns the tenant-local resources (integration
definitions, alert rule definitions, notification channel configuration).

Queries are routed through an adapter layer that speaks ClickHouse SQL,
PromQL, LogQL, and the Jaeger/Tempo APIs, so the API and UI don't know
or care whether a tenant is in push or BYO ingestion mode.

License: FSL-1.1-Apache-2.0.
