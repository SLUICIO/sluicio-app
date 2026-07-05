<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Alerts & notifications

| Field | Value |
|-------|-------|
| **Area** | Alert rules, instances, deliveries, notification channels & profiles |
| **Automation status** | Partial (rule rendering unit-tested; firing/delivery cases manual) |
| **Automated by** | [alertRule.test.ts](../../../frontend/src/lib/alertRule.test.ts) (condition rendering); `alerting/types_test.go` (window math) |
| **Last reviewed** | 2026-06-20 |

## Preconditions
- Stack up; at least one notification channel; seed telemetry for metric/trace rules. Admin (or rule-owning team member) for mutations.

## Alert rules

### Case 1 — Create a metric alert
- **Endpoint:** `POST /api/v1/alert-rules` (signal=metric)
- **Steps:** name/severity → metric_name, aggregation (avg/sum/max/min/count/p50/p95/p99), operator, threshold, for_window, optional attr filters + split_by → channels → optional bind to integration/service/team → save.
- **Expected:** Rule created; evaluator polls ClickHouse per window and fires instances to channels on breach.
- **Code:** `handlers_alerts.go:247` · **Automation:** Partial (needs seeded metrics).

### Case 2 — Create a log alert
- **Endpoint:** `POST /api/v1/alert-rules` (signal=log)
- **Steps:** log_spec: min_severity, body_contains, attr filters, comparison (`at_least`/`fewer_than`), window_seconds → channels → save.
- **Expected:** Fires when log volume/severity crosses the threshold; `fewer_than` is the drought direction. Condition string rendered per [alertRule.ts](../../../frontend/src/lib/alertRule.ts).
- **Code:** `handlers_alerts.go:247` (`validateLogRuleSpec`) · **Automation:** Partial.

### Case 3 — Create a trace alert (failed / latency / low-traffic)
- **Endpoint:** `POST /api/v1/alert-rules` (signal=trace)
- **Steps:** choose `trace_error_spec` (count), `trace_latency_spec` (threshold_ms + quantile), or `trace_volume_spec` (distinct traces + window) → bind to integration/service (required) → channels → save.
- **Expected:** Fires on the chosen trace condition. Window math capped at 30d (regression-tested in `alerting/types_test.go`).
- **Code:** `handlers_alerts.go:247,515` · **Automation:** Partial.

### Case 4 — Pushed health check
- **Endpoint:** `POST /api/v1/alert-rules` (source=pushed) + `POST /api/v1/services/{name}/health-checks/{id}/value`
- **Expected:** No metric_name/aggregation; an external POST of a value past threshold fires it. · **Code:** `handlers_alerts.go:142` · **Automation:** yes (scriptable).

### Case 5 — List / get / update / delete rules (with team visibility)
- **Endpoints:** `GET /api/v1/alert-rules[?service=&integration=]`, `GET/PUT/DELETE …/{id}`
- **Expected:** Admins see all; non-admins see org-wide + their team's rules; an invisible rule returns **404** (not 403). Update re-checks team ownership.
- **Code:** `handlers_alerts.go:213,273,301,355` · **Automation:** yes.

### Case 6 — Preview a rule before saving
- **Endpoint:** `POST /api/v1/alert-rules/preview`
- **Expected:** Returns current value, sample count, `breached`, threshold, and per-`split_by` rows — constrained to the caller's visible services.
- **Code:** `handlers_alerts.go:563` · **Automation:** Partial (needs live metrics).

## Instances & deliveries

### Case 7 — View instances & acknowledge / resolve
- **Endpoints:** `GET /api/v1/alert-instances`, `POST …/{id}/acknowledge`, `POST …/{id}/resolve`
- **Steps:** On Alerts → Instances, acknowledge a firing alert (stops re-notify) or resolve it (closes).
- **Expected:** State changes; auto-mode rules re-fire if the condition persists, manual-mode stay closed. Team-filtered for non-admins.
- **Code:** `handlers_alerts.go:654,718,735` · **Automation:** Partial (needs a firing rule).

### Case 8 — Delivery history
- **Endpoint:** `GET /api/v1/alert-deliveries` · **Expected:** Per-delivery rule, channel kind, recipient, status, timestamp; newest first. · **Code:** `:687` · **Automation:** Partial.

## Notification channels & profiles

### Case 9 — Channel CRUD + test
- **Endpoints:** `GET/POST/PUT/DELETE /api/v1/notification-channels[/{id}]`, `POST …/{id}/test`
- **Steps:** Create a channel (email / webhook / slack / pagerduty) with kind-specific config → Test (optional recipient override).
- **Expected:** Validated per kind; passwords never returned (only `password_set`); Test returns 204 on success / 502 with the underlying error. Email needs SMTP (see [platform-settings.md](platform-settings.md)).
- **Code:** `handlers_alerts.go:761,794,808,867` · **Automation:** Partial (delivery needs a reachable endpoint).

### Case 10 — Notification profiles (EE)
- **Endpoints:** `GET/POST/PATCH/DELETE /api/v1/notification-profiles[/{id}]`, `PUT …/{id}/channels`, `GET/PUT /api/v1/integrations/{id}/notification-profile`
- **Steps:** Create a profile (grouping per-check/per-integration, renotify_minutes, is_default) → set channels → assign to an integration (or inherit default).
- **Expected:** Drives grouping + re-notify cadence. **EE-only** — list returns empty when unlicensed.
- **Code:** `handlers_notification_profiles.go:30,44,142,194` · **Automation:** Partial (needs EE license).

## Notes
- Service-scoped alerts are hidden from users without visibility of that service (hard boundary).
