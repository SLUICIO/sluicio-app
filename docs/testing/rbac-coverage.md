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

## Known gaps (not yet automated)

- **Editor (org-wide) restriction matrix** — editor's exact ceiling vs admin
  (member management, cell settings) is asserted only incidentally.
- **Per-signal combinations beyond logs-only** — metrics-only, traces-without-
  messages (the analyst grant), and unions of multiple signal-scoped policies.
- **Share to a user by email who joins later** (pending-share resolution).
- **Operator vs org-admin split on multi-org cells** — single-org cells make
  them the same person; the operator-tab gating is only unit-covered.
- **Service-account tokens through RBAC** — tokens inherit the account's
  role; no e2e drives the API as a scoped service account.
- **MCP surface under a scoped token** — the loopback dispatch inherits REST
  RBAC by construction, but no test proves a viewer token through MCP.
- Attach-before-telemetry: a group attached to a **service-less** integration
  grants nothing until telemetry arrives (visibility flows through member
  services — see canSeeIntegration). Current behaviour, documented by
  protocol-group-visibility.spec's precondition; product decision pending on
  whether direct attachment should show the integration row.

Add new combinations to rbac.spec (API-driven, fast) or as full UI protocols
(protocol-*.spec, slower but end-to-end honest) — and add the row here; this
file is the human-readable index the specs don't give you.
