<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Telemetry ingest (OTLP)

| Field | Value |
|-------|-------|
| **Area** | OTLP ingest of traces, logs, metrics into the cell |
| **Automation status** | Partial (seed step automatable; assertions land in [health-services.md](health-services.md)) |
| **Automated by** | release-verification workflow seeds via `seed-traces` |
| **Last reviewed** | 2026-06-20 |

## Preconditions

- Stack up (`make dev-up`); `cell-ingest` listening on `:4318`.
- `INGEST_ALLOW_ANONYMOUS=true` for the anonymous cases; unset/false for the key-auth cases.

## Cases

### Case 1 — Ingest OTLP traces
- **Actor:** an instrumented app / agent · **Endpoint:** `POST :4318/v1/traces`
- **Steps:** Send an `ExportTraceServiceRequest` (e.g. `make seed-traces`).
- **Expected:** `200`; spans land in ClickHouse and surface under the service's Traces within seconds.
- **Automation:** yes (seed) — assertions in health-services.

### Case 2 — Ingest OTLP logs
- **Actor:** app / log forwarder · **Endpoint:** `POST :4318/v1/logs`
- **Steps:** Send an `ExportLogsServiceRequest` (`seed-traces` also sends logs).
- **Expected:** `200`; records appear on the service Logs tab and global Logs page.
- **Automation:** yes (seed).

### Case 3 — Ingest OTLP metrics
- **Actor:** app / scraper · **Endpoint:** `POST :4318/v1/metrics`
- **Steps:** Send an `ExportMetricsServiceRequest` (`seed-traces` also sends metrics).
- **Expected:** `200`; metrics appear in the service Metrics tab and the explorer.
- **Automation:** yes (seed).

### Case 4 — Anonymous ingest allowed (dev)
- **Actor:** unauthenticated sender · **Precondition:** `INGEST_ALLOW_ANONYMOUS=true`
- **Steps:** POST telemetry with no ingest key.
- **Expected:** Accepted (`200`).
- **Automation:** yes.

### Case 5 — Ingest requires a key (prod posture)
- **Actor:** sender · **Precondition:** `INGEST_ALLOW_ANONYMOUS=false`
- **Steps:** POST telemetry (a) with no key, (b) with a valid org ingest key.
- **Expected:** (a) rejected (401/403); (b) accepted and attributed to that org.
- **Automation:** partial — needs a created ingest key (see [orgs-access-tenancy.md](orgs-access-tenancy.md) Case "ingest keys").

### Case 6 — Per-org attribution / isolation at ingest
- **Actor:** two orgs' senders · **Precondition:** keys for org A and org B
- **Steps:** Send distinct telemetry under each org's key.
- **Expected:** Each org sees only its own telemetry (logical isolation in ClickHouse by org). Verified on the read side — see tenant-isolation case in [orgs-access-tenancy.md](orgs-access-tenancy.md).
- **Automation:** partial.

## Notes
- `seed-traces` (`make seed-traces`) sends a synthetic batch of all three signals; `seed-traces-loop` streams continuously. The release workflow seeds once before the e2e run so Health/Services/Logs/Metrics have data.
