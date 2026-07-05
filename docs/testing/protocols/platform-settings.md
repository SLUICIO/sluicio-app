<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Platform settings & definitions

| Field | Value |
|-------|-------|
| **Area** | Tags, metadata fields, dashboards, cell settings, license, audit |
| **Automation status** | Partial (most are admin CRUD; EE/SMTP cases manual) |
| **Automated by** | — |
| **Last reviewed** | 2026-06-20 |

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

## Notes
- EE gates: notification profiles, long retention (>14d), MFA-required policy, audit log. Verify Community builds hide/deny these and EE builds expose them.
