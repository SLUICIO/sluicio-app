<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Health, services & classification

| Field | Value |
|-------|-------|
| **Area** | Health dashboard, services list/detail, facets, tags, service metadata |
| **Automation status** | Partial (smoke-rendered in [smoke spec](../../../e2e/tests/smoke.spec.ts); data-dependent cases manual until seeded e2e lands) |
| **Automated by** | `e2e/tests/smoke.spec.ts` (render), release-verification seeds data |
| **Last reviewed** | 2026-07-25 |

## Preconditions
- Stack up; **seed telemetry** (`make seed-traces`) for any case that asserts counts/status.
- Signed in (seed admin = org admin).

## Cases

### Case 1 — Services list with windowed stats
- **Actor:** any role · **Endpoint:** `GET /api/v1/services?range=1h`
- **Steps:** Open Services; change the time range.
- **Expected:** Each service shows name, namespace, trace/error counts **for the window**, facets, status, tags, last-seen. Quiet services still appear with zero counts (catalog is window-independent). Sorted most-recently-active first.
- **Code:** `handlers.go:945` (`listServices`/`serviceSummaries`) · **Automation:** Partial (needs seed).

### Case 2 — Health status derivation
- **Actor:** system · **Computed in:** `handlers.go:1036,1142` (`computeServiceStatus`)
- **Steps:** Observe a service's status pip after seeding errors / configuring checks.
- **Expected:** `ok` (no errors/firing checks), `errors` (window error count > 0 or firing non-critical check), `quiet` (no traces in window), `unhealthy` (firing critical check or open unacknowledged errors). Watermark from cleared errors is honored.
- **Automation:** Partial (needs ClickHouse + error-ack state).

### Case 3 — Service detail
- **Actor:** any role · **Endpoint:** `GET /api/v1/services/{name}?range=1h`
- **Steps:** Open a service.
- **Expected:** Profile, window stats (counts, error rate, p50/p95), golden-signal sparklines, integrations, tags, recent spans, error-ack info.
- **Code:** `handlers.go:1283` (`serviceDetail`) · **Automation:** Partial.

### Case 4 — Neighbors (callers/callees)
- **Actor:** any role · **Endpoint:** `GET /api/v1/services/{name}/neighbors`
- **Expected:** Inbound/outbound services with per-edge trace/error counts in the window.
- **Automation:** Partial (needs multi-service traces).

### Case 5 — Clear / acknowledge service errors
- **Actor:** editor/admin · **Endpoint:** `POST` & `DELETE /api/v1/services/{name}/clear-errors`
- **Steps:** On a service with errors, Clear errors (+ optional note); later un-clear.
- **Expected:** Watermark stored; window error counts exclude pre-watermark errors; status drops from `unhealthy`→`ok/errors`; who/when shown.
- **Code:** `handlers.go:587` · **Automation:** Partial.

### Case 6 — Health-check readings & pushed values
- **Actor:** external system · **Endpoints:** `GET /api/v1/services/{name}/readings`, `POST /api/v1/services/{name}/health-checks/{id}/value`
- **Steps:** Configure a `source=pushed` check; POST a value past threshold.
- **Expected:** Reading tiles show value/operator/threshold/breach; a breach fires its alert + notification.
- **Code:** `handlers.go:584,585` · **Automation:** yes (scriptable POST).

### Case 7 — Facet auto-detection + widgets
- **Actor:** any role · **Endpoints:** `GET /api/v1/service-facets`, `/services/{name}/widgets`
- **Steps:** Open Service Facets; open a facet to see classified services; open a service's widgets.
- **Expected:** Facets carried per service tagged `auto`/`manual`; each effective facet renders its widgets (missing data = flat sparkline, non-fatal).
- **Code:** `handlers_service_types.go:42,136` · **Automation:** Partial.

### Case 8 — Manual facet overrides
- **Actor:** admin · **Endpoints:** `GET/PUT /api/v1/services/{name}/facet-overrides`
- **Steps:** Toggle include/exclude on facets; save.
- **Expected:** Override persists atomically; `core` is always-on/non-removable; effective set = auto ∪ include − exclude. (Unit-tested in [handlers_facet_overrides_test.go](../../../services/cell-api/internal/api/handlers_facet_overrides_test.go).)
- **Code:** `handlers_facet_overrides.go:51,66` · **Automation:** yes.

### Case 9 — Facet mapping rules
- **Actor:** admin · **Endpoints:** `GET/POST/DELETE /api/v1/services/{name}/facet-mappings`
- **Steps:** Add a rule (attribute source/key/operator/value → io_kind/io_role); list; delete.
- **Expected:** Rule compiles into the service's facet classification (SQL CASE); applied to subsequent queries.
- **Code:** `handlers_facet_mappings.go:30,54,91` · **Automation:** yes.

### Case 10 — Service tags
- **Actor:** admin · **Endpoints:** `GET/POST/DELETE /api/v1/services/{name}/tags/{tagId}`
- **Steps:** Attach/detach a tag; filter services by tag.
- **Expected:** Tag persists by `(org, service_name)` (survives quiet periods); attach is idempotent.
- **Code:** `handlers_tags.go:225,241,269` · **Automation:** yes.

### Case 11 — Service metadata (built-in + custom)
- **Actor:** admin · **Endpoints:** `GET/PUT /api/v1/services/{name}/metadata`, `PUT …/metadata-extras`
- **Steps:** Edit description/owner/on-call/team/repo/runbook; set custom field values.
- **Expected:** Values persist and render on detail; runbook URL validated as http(s); in/out schemas shown.
- **Code:** `handlers_service_metadata.go:15,65` · **Automation:** yes.

## System types (shareable, v0.11.28+)

### Case 12 — Browse the catalog and read a type
- **Surface:** System types page. **Endpoint:** `GET /api/v1/system-types`
- **Steps:** Click anywhere on a type's row.
- **Expected:** The row expands to show its detection prefixes and every starter check with a readable condition summary (no "undefined"). Clicking Export/Edit/Delete does NOT collapse the row. Built-ins are marked read-only and carry a **"Docs ↗"** button that opens `docs.sluicio.com/system-types/<key>/` — check a couple resolve (all 11 built-ins have pages).
- **Automation:** yes — `e2e/tests/krakend-template.spec.ts`, `systems.spec.ts`.

### Case 13 — Export / import a system type
- **Endpoints:** `GET /api/v1/system-types/{key}/export`, `POST /api/v1/system-types/import[?replace=true]`
- **Steps:** Export any type (built-ins included) → edit the `key` in the file → import it → import it a second time unchanged.
- **Expected:** Download is `<key>.systemtype.yaml` (`format: sluicio/system-type/v1`). Import validates strictly and creates a custom type; a repeat import of the same key returns **409** unless replace is used. Importing with a BUILT-IN's key creates your org's override of it (removing the override restores the built-in).
- **Automation:** yes — `e2e/tests/system-types-share.spec.ts`.

### Case 14 — Auto-detection and applying starter checks
- **Steps:** Send telemetry whose metric names match a type's prefixes (e.g. `kafka.`, `wso2`/`org_wso2`, `gnatsd_`, `debezium`) → open the service → apply the suggested template → remove it again.
- **Expected:** The type is suggested from the metric names alone; applying creates its checks as ordinary, tunable alert rules on that service (apply reports the created count); remove deletes exactly those checks. Applying twice is a no-op rather than a duplicate.
- **Note:** 11 built-ins today — RabbitMQ, ActiveMQ Artemis, Azure Service Bus, KrakenD, **Apache Kafka, Confluent Kafka, NATS, Debezium, WSO2 API Manager**, OTel Collector, .NET service.
- **Automation:** Partial — apply/remove automated (`wso2-apim-template.spec.ts`, `azure-servicebus-template.spec.ts`, `krakend-template.spec.ts`); detection from live telemetry is manual.

## Notes
- Tag/metadata definitions themselves live in [platform-settings.md](platform-settings.md).
