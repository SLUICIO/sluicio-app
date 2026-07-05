<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Traces, logs, metrics, search & topology

| Field | Value |
|-------|-------|
| **Area** | The telemetry exploration surfaces |
| **Automation status** | Partial (routes render in smoke; data-dependent assertions manual until seeded e2e) |
| **Automated by** | `e2e/tests/smoke.spec.ts` (render) |
| **Last reviewed** | 2026-06-20 |

## Preconditions
- Stack up; **seed telemetry** (`make seed-traces`); signed in.

## Cases

### Case 1 — Trace waterfall
- **Actor:** any role · **Endpoint:** `GET /api/v1/traces/{traceId}`
- **Steps:** Open a trace (from search, a service's Traces tab, or `/traces/{id}`).
- **Expected:** Spans ordered by time, parent/child tree reconstructed; merged resource+span attributes; span-kind shown; truncation warning past the 5000-span cap; trace-completion status if the integration has SLA rules.
- **Code:** `handlers.go:1558` (`traceDetail`) · **Automation:** Partial.

### Case 2 — Service traces list (failed-only filter)
- **Actor:** any role · **Endpoint:** `GET /api/v1/services/{name}/traces?range=1h&only_failed=`
- **Expected:** Recent traces involving the service (newest first): id, start, duration, span/service counts, has-error, first span; `only_failed=true` narrows to error traces.
- **Code:** `handlers.go:1510` · **Automation:** Partial.

### Case 3 — Trace-completion / SLA stages
- **Actor:** any role (view); admin (rules) · **Endpoints:** `GET /api/v1/integrations/{id}/completion-rules`, `…/completion-counts`, `…/completion-firings`
- **Steps:** Configure a rule (start span → ordered stages with timeouts); send traces that complete late.
- **Expected:** Counts show completed / pending / delayed; delayed traces surface as firings; the count is start-span-gated so success rate never underflows; a delayed firing can be marked handled.
- **Code:** `handlers_trace_completion.go:104,277,339` · **Automation:** Partial (needs multi-stage traces). See [enhancement notes](../../../docs/decisions.md) and memory `enhancement-multistage-trace-completion`.

### Case 4 — Service & global logs with filters
- **Actor:** any role · **Endpoints:** `GET /api/v1/services/{name}/logs`, `GET /api/v1/logs`
- **Steps:** Filter by `q` (body substring), `min_severity`, `service`/`integration`, `trace_id`, `attr_*`.
- **Expected:** Newest-first entries with timestamp/severity/body/attributes/trace link; default limit 100 (service) / 200 (global), max 1000; keyset pagination. (Severity bucketing unit-tested in [severity.test.ts](../../../frontend/src/lib/severity.test.ts).)
- **Code:** `handlers_logs.go:18,86` · **Automation:** Partial.

### Case 5 — Log fields & values (filter builder)
- **Actor:** any role · **Endpoints:** `GET /api/v1/log-fields`, `GET /api/v1/log-attributes/{key}/values`
- **Expected:** Field keys (type-tagged); top-N values per key with event counts — drives the add-filter UI.
- **Automation:** Partial.

### Case 6 — Metrics catalog & series
- **Actor:** any role · **Endpoints:** `GET /api/v1/metric-names`, `/metric-series`, `/services/{name}/metric-names`
- **Steps:** Browse metric catalog; chart a metric.
- **Expected:** Distinct metrics with type/unit/emitting-service counts; selecting one charts a bucketed series (~120 points), multi-line by service on the global page.
- **Code:** `handlers_otlp_metrics.go:31,60,94` · **Automation:** Partial.

### Case 7 — Metrics explorer with attribute filters
- **Actor:** any role · **Endpoints:** `GET /api/v1/metric-catalog`, `/metric-fields`, `/metric-attributes/{key}/values`
- **Steps:** Filter by name substring, OTLP type, and attribute key/value.
- **Expected:** Sparkline table (type-aware) with headline value, series count, rule badges; row expands to per-service breakdown.
- **Code:** `handlers_metrics_explorer.go:31,135,164` · **Automation:** Partial.

### Case 8 — Global search
- **Actor:** any role · **Endpoint:** `GET /api/v1/global-search?q=…`
- **Steps:** Type in the top-bar search.
- **Expected:** Results grouped (integrations, services, messages/spans, logs, metrics), prefix-ranked, ≤10 per group with "see more"; each scope policy-filtered to what the caller may see.
- **Code:** `handlers_global_search.go:71,141` · **Automation:** Manual (interactive).

### Case 9 — Span search
- **Actor:** any role · **Endpoint:** `GET /api/v1/search?q=…&only_failed=&integration=&service=`
- **Expected:** Matching **traces** (trace is the unit) with which span/service matched; newest first.
- **Code:** `handlers.go:1401` · **Automation:** Partial.

### Case 10 — Topology / integration flow graph
- **Actor:** any role · **Endpoint:** `GET /api/v1/integrations/{id}/flow?range=1h`
- **Expected:** Service nodes colored by health; edges labeled with call/error counts; schema + map overlays on edges; falls back to ~90-day historical topology when the window has no hops.
- **Code:** `handlers_integrations.go:314,443` · **Automation:** Partial.

## Notes
- Time window: defaults to 1h; catalog/discovery is window-independent, activity is window-bounded; the Errors feed uses the retention lookback (≈30d), not the active window.
