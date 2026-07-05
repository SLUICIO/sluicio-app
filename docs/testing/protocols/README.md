<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Use-case catalog

The documented set of **use cases** Sluicio promises to support. This is
the source of truth for release verification: every use case here is
walked (manually) or run (automated) **before a release is tagged** —
not on every build. Fast correctness checks (build, vet, unit, lint,
typecheck, component, integration) stay on per-push CI; the use-case
suite is the release gate.

See [../README.md](../README.md) for the layered strategy and
[../release-acceptance.md](../release-acceptance.md) for the checklist
that drives a release sign-off.

## How to use this catalog

- **At release:** open [../release-acceptance.md](../release-acceptance.md)
  and work top to bottom. For each area it points here; walk the
  `Manual` cases and confirm the `Automated` ones are green
  (the `release-verification` workflow runs them on the tag, or trigger
  it by hand from the Actions tab).
- **When you change a flow:** update its case here, and its Playwright
  spec if automated. Case numbers in a protocol match the `test()` names
  in the paired spec so the two never drift.
- **Automation status per case:** `Automated` (a spec runs it),
  `Partial` (spec covers part), or `Manual` (walked by a human — often
  because it needs an external dependency like a real OIDC IdP, SMTP, a
  TOTP secret, or an EE license).

## Areas

| # | Protocol | Covers |
|---|----------|--------|
| 1 | [auth-login.md](auth-login.md) | Email+password login, logout, session, bad creds |
| 2 | [auth-account-mfa.md](auth-account-mfa.md) | Account profile, password change, forgot/reset, MFA enroll/login/disable |
| 3 | [orgs-access-tenancy.md](orgs-access-tenancy.md) | Orgs, members, roles, groups & policies, API tokens, ingest keys, tenant isolation |
| 4 | [telemetry-ingest.md](telemetry-ingest.md) | OTLP ingest of traces/logs/metrics, ingest-key auth, anonymous mode |
| 5 | [health-services.md](health-services.md) | Health dashboard, services list/detail, neighbors, readings, health-check push, facets/overrides, tags, metadata |
| 6 | [traces-logs-metrics.md](traces-logs-metrics.md) | Trace waterfall, trace-completion, logs surface, metrics explorer, global search, topology |
| 7 | [integrations-messages.md](integrations-messages.md) | Integration CRUD, matcher rules, completion rules, messages, message-views, stuck, errors/acks, schemas, maps |
| 8 | [alerts-notifications.md](alerts-notifications.md) | Alert rules + preview, instance ack/resolve, deliveries, notification channels/profiles, SMTP test |
| 9 | [platform-settings.md](platform-settings.md) | Cell settings (retention/security/SMTP/system), dashboards, license, audit log (EE) |
| 10 | [operator.md](operator.md) | Cell operator (super-admin): org lifecycle, cross-org member assignment, operator promote/demote, cell-wide-settings gating |

## Roles (used in "Actor" throughout)

Per `(user, org)`: **viewer** (read), **editor** (read + mutate
resources), **admin** (+ manage members, tokens, SSO, settings). The
seed admin `admin@sluicio.local` is an `admin` in the seeded org — and,
on a fresh cell, is auto-promoted to **operator**, the cell super-admin
above the org roles (see [operator.md](operator.md)).
