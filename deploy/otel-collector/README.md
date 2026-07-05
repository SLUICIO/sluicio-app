# OTel Collector recipes

Example OpenTelemetry Collector configurations that ship telemetry to an
Integration Monitor cell. These are templates customers adapt to their
environment.

License: Apache-2.0.

## Files

- [`push-to-cell.yaml`](./push-to-cell.yaml) — minimal push-mode config:
  receive OTLP locally, forward to the cell's OTLP endpoint with a
  bearer token.
