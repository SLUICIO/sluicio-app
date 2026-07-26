<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Platform settings & definitions

| Field | Value |
|-------|-------|
| **Area** | Tags, metadata fields, dashboards, cell settings, license, audit |
| **Automation status** | Partial (most are admin CRUD; EE/SMTP cases manual) |
| **Automated by** | — |
| **Last reviewed** | 2026-07-25 |

## Preconditions
- Stack up; signed in as **admin** (most mutations are admin-only). EE cases need an `SLUICIO_LICENSE_KEY`; SMTP cases need a reachable mail server.

## Definitions

### Case 1 — Tags CRUD
- **Endpoints:** `GET/POST/PATCH/DELETE /api/v1/tags[/{id}]` (`?include=usage`)
- **Steps:** Create (slug auto-slugified, name, hex color) → list (optionally with usage counts) → edit name/color → delete (shows cascade count).
- **Expected:** Slug unique per org and **immutable** (saved searches stay valid); delete cascades `integration_tags`/`service_tags`.
- **Code:** `handlers_tags.go:30,76,106,144` · **Automation:** yes.

### Case 2 — Metadata fields
- **Endpoints:** `GET/POST/PATCH/DELETE /api/v1/metadata-fields[/{id}]`
- **Steps:** Define a field (key unique; type string/number/boolean/single-select/multi-select; scope integration/service/both; required; options) → edit → delete.
- **Expected:** Type/scope combos validated; edits checked against existing values; delete clears values on integrations/services. Set values via integration/service metadata (see [integrations-messages.md](integrations-messages.md) / [health-services.md](health-services.md)).
- **Code:** `handlers_metadata.go:19,30,49,77` · **Automation:** yes.

### Case 3 — Dashboards
- **Endpoints:** `GET/POST/PUT/DELETE /api/v1/dashboards[/{id}]`
- **Steps:** Create (name/description/order, optional integration items) → list → view → update (full replace of items) → delete.
- **Expected:** Items are a set keyed by dashboard+integration; PUT drops anything not in the payload; widgets lazy-load.
- **Code:** `handlers_dashboards.go:16,51,71,105` · **Automation:** yes.

## Cell settings

### Case 4 — Retention policy
- **Endpoints:** `GET/PATCH /api/v1/cell-settings/retention`
- **Steps:** View traces/logs/metrics retention days + last-applied; edit within min/max bounds.
- **Expected:** Saved + applied to ClickHouse (`RetentionEnforcer.ApplyOnce`); beyond the free cap (14d) requires EE → otherwise **402 Payment Required**; `apply_warning` surfaced if the live apply failed.
- **Code:** `handlers_cell_settings.go:90,121` · **Automation:** Partial (long retention is EE).

### Case 5 — System settings
- **Endpoints:** `GET/PATCH /api/v1/cell-settings/system`
- **Steps:** Edit environment label (top-nav) + ingest base URL (exporter snippets).
- **Expected:** Validated; empty fields mean "keep current"; change recorded in audit.
- **Code:** `handlers_cell_settings.go:253,268` · **Automation:** yes.

### Case 6 — SMTP config + test
- **Endpoints:** `GET/PATCH /api/v1/cell-settings/smtp`, `POST …/smtp/test`
- **Steps:** Enter host/port/username/password/from/from_name → Test (optional recipient).
- **Expected:** `password_set`/`configured` flags returned (never the password); Test returns 204 / 502 with the error. Required for email channels + password reset.
- **Code:** `handlers_cell_settings.go:347,415` · **Automation:** Partial (needs SMTP).

### Case 7 — Security: MFA-required policy (EE)
- **Endpoints:** `GET/PATCH /api/v1/cell-settings/security`
- **Steps:** Toggle "MFA required".
- **Expected:** When on, any user without MFA is forced to enroll on next login (`mfaEnrollmentRequired`); change audited. **EE-gated** (`mfa_policy_entitled`).
- **Code:** `handlers_cell_settings.go:450` · **Automation:** Partial (EE).

## License & audit

### Case 8 — License status
- **Endpoint:** `GET /api/v1/license` · **Expected:** Features map + entitlements; all-false/empty when unlicensed or expired. · **Code:** `handlers_license.go:42` · **Automation:** yes.

### Case 9 — Audit log (EE)
- **Endpoint:** `GET /api/v1/audit-log?limit=&before=&actor=&actor_id=&action=&target_type=&target=&from=&to=`
- **Expected:** Newest-first entries (actor, action e.g. `retention.update`, target, metadata, IP, time); keyset-paginated via `before`. Filters combine: `actor` is a case-insensitive name/email substring, `action` a prefix (`member.` matches `member.added` …), `from`/`to` RFC3339 bounds on occurred_at — so "what did X do between 8 and 10" is one query. Invalid `actor_id`/`from`/`to` → 400. **EE-only** — gated by the `audit_log` entitlement; admin actions recorded via `recordAudit`, auth events (login/logout/password/MFA) via `recordAuthAudit` (written once per org membership).
- **Code:** `handlers_audit.go` · **Automation:** Partial (EE).

### Case 10 — Audit log search UI (EE)
- **Surface:** Settings → Audit log tab.
- **Expected:** Filter bar (Actor, Action with prefix suggestions, From, To) live-filters the table; Clear resets; scrolling near the bottom of the table lazy-loads the next keyset page (under the active filters); clicking a row expands a detail view with the full entry JSON (metadata, target, IP); "Export CSV" downloads the filtered entries (`format=csv`, capped at 50k rows).
- **Automation:** yes — `e2e/tests/audit.spec.ts` (self-skips without the `audit_log` entitlement).

### Case 11 — Operator actions visible to the target org (EE)
- **Behaviour:** operator org/member mutations are dual-written: once to the operator's own org log and once to the *target* org's log, so tenant admins can see cell-operator changes to their org (`org.deleted` is single-write — the target log dies with the org).
- **Automation:** manual (needs two orgs).

### Case 12 — Audit tamper evidence + retention (EE)
- **Chain:** every entry is hash-chained per org (`entry_hash = sha256(prev ∥ fields)`, migration 0059). `GET /api/v1/audit-log/verify` (or the "Verify integrity" button) walks the chain: edits report `content hash mismatch`, deletions `chain link mismatch`, both with the first broken id. Pre-0059 entries count as `legacy_unhashed`, not failures.
- **Retention:** `audit_days` on the retention settings (default **14**, EE `audit_log` entitlement unlocks up to 3650). The hourly enforcer prunes Postgres rows chain-safely (the last pruned hash is kept as the verification anchor). Non-EE raising past 14 → 402.
- **Off-box sink:** deploy-time only — `SLUICIO_AUDIT_SINK_URL` (+ optional `SLUICIO_AUDIT_SINK_SECRET` for HMAC-signed requests) on cell-api. Off by default; never configurable via the API. See docs/audit-log.md.
- **Automation:** chain verify + retention round-trip in `e2e/tests/audit.spec.ts`; sink in `pkg/audit/sink_test.go`; tamper drill manual (requires DB access).

## Usage report & trim ingestion (v0.11.39+)

### Case 13 — The usage report reads across all three signals
- **Surface:** Settings → Reports. **Endpoint:** `GET /api/v1/reports/usage?range=` (admin-only)
- **Steps:** Open the tab with seeded telemetry; switch the range selector (1h / 24h / 7d / 30d).
- **Expected:** Three savings cards ("We found X of Y metrics that aren't used in any alert. Trimming them could save ≈ Z/day (≈ W/month)") for metrics, logs and traces; below them the unused-metric table, then "Logs by service" and "Traces by service" with uncovered services first, each showing rows + estimated size + a covered/not-covered flag. Sizes are estimates from compressed table averages. A viewer gets **403** on the endpoint.
- **Automation:** yes — `e2e/tests/usage-report.spec.ts`.

### Case 14 — Metric attribute breakdown
- **Steps:** In the unused-metrics table, click a metric row.
- **Expected:** It expands to that metric's datapoint attributes — keys with use-count and cardinality; expanding a key lists its top values with counts. (Uses the existing metric-fields endpoints; no new data required beyond metrics with attributes.)
- **Automation:** yes — `e2e/tests/metrics-trim-attrs.spec.ts`.

### Case 15 — Trim ingestion generates a collector config
- **Surface:** Settings → Reports → "✂ Trim ingestion".
- **Steps:** **Metrics tab** — tick metrics, accept a prefix suggestion, then use "attrs ▸" on a row and pick a value with the ✂ button. **Logs tab** — tick a service and choose a severity floor. **Traces tab** — tick a service; observe the mode (sample vs drop) and the ⚠ flag.
- **Expected:** The YAML pane updates live: `metric:` name/IsMatch conditions, a `datapoint:` section for attribute rules, a `logs:` section whose conditions carry `severity_number < SEVERITY_NUMBER_WARN` for a floor, a `traces:` span section for drops, and a `tail_sampling/sluicio-trim` processor when sampling. Services feeding an integration are flagged ⚠ and default to **sample** (dropping their spans would blind that integration's health checks). Only the pipelines that gained a processor appear under `service.pipelines`. Copy puts the config on the clipboard.
- **Expected (important):** Sluicio enforces none of this — it's advisory config for your own collector.
- **Automation:** Partial — `e2e/tests/usage-report.spec.ts` + `metrics-trim-attrs.spec.ts` cover generation; pasting into a real collector is manual.

## Announcements (v0.11.35+, consolidated v0.11.37)

### Case 16 — Cell-wide announcement + login-page banner
- **Surface:** Settings → System → Cell-wide announcements. **Endpoints:** `GET/POST/DELETE /api/v1/operator/announcements`, public `GET /api/v1/announcements/login`
- **Steps:** Publish an announcement with severity + expiry; tick **"Show on login page"** → sign out.
- **Expected:** In-app banner for every user (dismissal is per-user and sticks across reloads); the flagged one ALSO renders on the sign-in page before login, where dismissal is per-browser (localStorage). Unflagged announcements never appear pre-login. The public endpoint exposes only message/severity/dismissible — no org, author or timing metadata.
- **Note:** There is exactly ONE announcement surface since v0.11.37 (the Settings → Organization section was removed; `/api/v1/settings/announcements` now 404s).
- **Automation:** yes — `e2e/tests/maintenance.spec.ts`.

## Notes
- EE gates: notification profiles, long retention (>14d), MFA-required policy, audit log. Verify Community builds hide/deny these and EE builds expose them.
