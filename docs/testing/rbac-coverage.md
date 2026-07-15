<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# RBAC test coverage — the executed list

What the automated suite verifies about access control, mapped to the RBAC v2
model (docs/rbac-v2-design.md), and where the gaps are. **Every test below runs
on every release tag** (release-verification spins a fresh seeded cell in CI
and executes the full Playwright suite) — a red gate blocks the tag.

To print the literal list of tests any run will execute:

```sh
cd e2e && npx playwright test --list          # all (105 tests / 26 files today)
cd e2e && npx playwright test --list rbac     # the RBAC spec alone
```

## The matrix, as covered today

| Model area | Edition | Verified behaviour | Test |
|---|---|---|---|
| Deny-by-default | CE | Group-less viewer sees **no** services | rbac.spec `viewer restrictions › group-less viewer sees no services` |
| Role capability | CE | Viewer cannot read the member list | rbac.spec `viewer cannot read the member list` |
| Role capability | CE | Viewer cannot mutate message views | rbac.spec `viewer cannot mutate message views` |
| Role capability | CE | Viewer cannot mutate config / reach admin surfaces | rbac.spec `viewer cannot mutate config…` |
| Group attach (visibility grant) | CE | Attach group → members see integration + its services | rbac.spec `attaching a group to an integration grants…` |
| Group attach | CE | Same for systems | rbac.spec `attaching a group to a system grants…` |
| Group attach | CE | Foreign group ids rejected | rbac.spec `foreign group ids are rejected` |
| Group attach, full UI protocol | CE | Admin creates integration + user + group, attaches, signs out; viewer signs in and sees exactly the granted integration | protocol-group-visibility.spec |
| Expression policies | EE | Prefix expression scopes viewer to exactly matching services | rbac.spec `service-name prefix scopes…` |
| Expression policies | EE | NOT excludes from an otherwise-granting tree | rbac.spec `NOT excludes a service…` |
| Expression policies | EE | Malformed expression → 400 | rbac.spec `malformed expression is rejected` |
| Scoped manage | EE | Group-editor: in-scope edit 200, out-of-scope 404 (invisible), out-of-scope matcher 403, class-A org config 403, team dashboard allowed | rbac.spec `group-editor manages exactly the scoped service` |
| Resource sharing | EE | Share → view-only for grantee, digest lists it, revoke removes; system parity; duplicate rejected | rbac.spec `share → grantee sees…`, `system share parity…` |
| Per-signal visibility | EE | Logs-only grant: service visible, logs flow, traces/metrics empty, zero manage | rbac.spec `logs-only grant…` |
| Per-signal visibility | EE | Unknown signal → 400 | rbac.spec `unknown signal rejected` |
| Leakage via graphs | — | Scoped viewer's dependency graph hides out-of-scope neighbors | rbac.spec `scoped viewer sees no out-of-scope neighbors` |
| Entitlement gating | CE | EE surfaces (SSO, audit, policies, MFA policy…) upsell instead of function | ce-upsell.spec (per-surface tests) |
| Entitlement gating | EE | Licensed cell exposes all five entitlements; retention beyond free cap | ee-features.spec |
| Org-wide MFA policy | EE | Unenrolled member locked to enrollment; enabling while unenrolled refused | ee-features.spec |
| Demo accounts | — | is_demo users blocked from mutations | demo-account.spec |
| Forced password rotation | — | must_reset user locked to the change screen | force-password.spec |
| Audit access | EE | Audit surfaces admin-gated; access itself audited | audit.spec |
| Tenant isolation | — | Org A cannot read org B (API level) | Go: tenant_isolation_integration_test.go |
| Badge auth | — | Public status badges leak nothing beyond the badge | Go: badge_authz_integration_test.go |

### Gap closure, 2026-07-15

All seven gaps above were automated (rbac.spec, new describes at the end of
the file). Closing them surfaced — and fixed or filed — three findings:

| Model area | Edition | Verified behaviour | Test |
|---|---|---|---|
| Editor ceiling | CE | Editor creates/deletes integrations; member admin, group admin, cell settings all 403 | rbac.spec `org editor ceiling` |
| Operator split | — | A second (non-operator) org admin manages members but cell-wide settings refuse (server-side RequireOperator) | rbac.spec `non-operator admin` |
| Per-signal | EE | Metrics-only grant: metrics flow, logs and messages empty | rbac.spec `metrics-only grant…` |
| Per-signal | EE | Messages-only grant: business lens works, logs and metrics empty | rbac.spec `messages-only grant…` |
| Sharing | EE | Grantee must be an existing org member — unknown email rejected (**no pending shares by design**; the earlier "joins later" gap was a misreading) | rbac.spec `sharing to an unknown email…` |
| Service accounts | — | Scoped by default: group-less SA sees **nothing**; group membership grants exactly the group's scope; writes/admin stay role-gated | rbac.spec `scoped viewer SA…` |
| Service accounts | — | `org_wide` is an explicit opt-in that reads the whole org | rbac.spec `org-wide SA is an explicit opt-in…` |
| Service accounts | — | Forbid-org-wide cell setting: creation rejected, existing org-wide SAs resolve as scoped | rbac.spec `forbid-org-wide cell setting…` |
| Service accounts | — | Same resolver for users and SAs at the store layer (membership, cascade, isolation between the kinds) | Go: service_accounts_integration_test.go |
| MCP | — | MCP mirrors REST exactly for the same token (admin PAT sees all; group-less scoped viewer-SA sees zero) | rbac.spec `MCP surface` |
| Attach-before-telemetry | CE | Group attached to a service-less integration grants nothing until telemetry arrives — pinned current semantics | rbac.spec `attach before telemetry` |

### Dashboards + alerting, 2026-07-15

| Model area | Edition | Verified behaviour | Test |
|---|---|---|---|
| Dashboards | — | Team dashboard invisible to non-members (list + direct GET → 404); org-wide visible to all but read-only for viewers (canManage=false, PUT/DELETE 403) | dashboards-rbac.spec `team dashboard is invisible…` |
| Dashboards | EE | Team editor: full lifecycle on their team's boards; org-wide create and other teams' boards refused | dashboards-rbac.spec `team editor manages exactly…` |
| Dashboards / widgets | — | Widget data cannot leak: an org-wide dashboard referencing an out-of-scope integration renders no widget for a scoped viewer — /integrations is filtered, direct fetch 404s, and the Health page shows only the granted card | dashboards-rbac.spec `widget data never leaks…` |
| Dashboards × SAs | — | Scoped service accounts see team dashboards only via group membership (same rule as users) | dashboards-rbac.spec `scoped service account sees…` |
| Alerting | — | Team-owned alert rules invisible to group-less viewers | alert-lifecycle.spec `recipient + trigger configuration…` |
| Alerting (functional) | — | Error traces on an integration fire a threshold rule within one engine tick (~30s) and deliver ONLY to the rule's bound channels (proven by a live webhook sink); the error notifier pages the integration's notification-profile channels within its 60s cadence | alert-lifecycle.spec (needs `E2E_INGEST_URL`; skips without reachable ingest) |
| Alerting (channels) | — | Every creatable channel kind actually delivers via the real test endpoint: webhook (canonical payload), Slack (state-prefixed text), PagerDuty (Events v2 trigger via `events_url` override — also the EU-region knob), email over real SMTP (multipart, correct subject); unreachable destinations surface errors, never false success; opt-in webhook HMAC signing verified end-to-end the way docs/webhook-signing.md tells receivers to (and secret-less channels stay unsigned). Wire formats + header-injection safety pinned by Go unit tests | notification-channels.spec (email leg needs `E2E_SMTP_HOST`+`E2E_MAILPIT_API`); Go: alerting/delivery_test.go |

Findings from the closure:

- **Fixed — metric-catalog per-signal leak**: `/api/v1/metric-catalog` used the
  signal-agnostic service filter and returned metric values to viewers whose
  grant excluded the metrics signal. Now gated through the metrics tier like
  every other metrics endpoint (the messages-only test pins it).
- **Filed — service-account visibility** (issue #2): SA principals bypass
  deny-by-default and read org-wide; role gates hold for writes.
  **Resolved 2026-07-15** — SAs are now first-class group members, scoped
  by default (docs/service-account-scoping-design.md); the pinned tests
  above were flipped with the change.
- **Corrected** — pending shares don't exist by design; the automated test
  asserts the rejection instead.

Add new combinations to rbac.spec (API-driven, fast) or as full UI protocols
(protocol-*.spec, slower but end-to-end honest) — and add the row here; this
file is the human-readable index the specs don't give you.
