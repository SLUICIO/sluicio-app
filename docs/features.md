<!-- SPDX-License-Identifier: Apache-2.0 -->

# Sluicio feature matrix

The canonical list of shipped features, split by edition. **Community**
is everything in the open-source product; **Enterprise** is the five
license-gated entitlements (`pkg/license`: `sso`, `rbac_advanced`,
`audit_log`, `retention_long`, `mfa_policy`). Each feature has a stable
slug — other trackers (docs, announcements, marketing) key off it, so
don't rename slugs; add new rows when features ship.

Maintenance rule: when a release adds a user-visible feature, add its
row here in the same change (the same convention as the use-case
catalog in docs/testing/protocols/).

## Community

### Telemetry ingest & exploration

| Slug | Feature | What it is |
|---|---|---|
| `otlp-ingest` | OTLP/HTTP ingest | Traces, logs, and metrics into ClickHouse via a dedicated stateless ingest service (protobuf OTLP) |
| `ingest-keys` | Per-org ingest keys | Mint/revoke keys; Bearer or `X-Sluicio-Ingest-Key`; anonymous mode for local dev |
| `ingest-5xx-normalize` | Treat HTTP 5xx as errors | Opt-in ingest-time span-status normalization for emitters (API gateways) that leave 5xx spans unmarked |
| `traces-explorer` | Trace search & detail | Full-trace view with span tree, origin-aware breadcrumbs |
| `logs-explorer` | Logs explorer | Severity/attribute/service filters, shareable filter URLs, log→trace blade |
| `metrics-explorer` | Metrics explorer | Metric catalog, series charts, usage view |
| `search` | Global search | Cross-signal search |

### Modelling your landscape

| Slug | Feature | What it is |
|---|---|---|
| `service-catalog` | Service catalog | Services auto-discovered from telemetry; metadata, ownership, facets |
| `integrations` | Integrations | Matcher-based grouping of services into business integrations; health rollup, per-integration messages/logs/errors |
| `systems` | Systems | Infrastructure entities (brokers, gateways) as first-class peers of integrations |
| `system-types` | System types & auto health checks | Built-in kinds (RabbitMQ, ActiveMQ Artemis, KrakenD, Azure Service Bus, OTel Collector, .NET service) with templated health checks; org-level overrides |
| `monitoring-templates` | Monitoring templates | Reusable check bundles applied per system |
| `tags-metadata` | Tags & metadata fields | Org-defined tags, typed metadata fields, schemas; searchable (`meta:` filters) |
| `service-facets` | Service facets | Facet classification with manual overrides |
| `topology` | Dependency graph & maps | Service dependency topology, visibility-filtered |

### Health & alerting

| Slug | Feature | What it is |
|---|---|---|
| `health-dashboards` | Health dashboards | Card-based dashboards with per-integration widgets (traffic sparkline, error count, …); org-wide and team-scoped |
| `alert-rules` | Alert rules | Metric / log / trace signals; trace kinds: error, latency, volume (dead-man's switch), completion; thresholds + windows; attribute predicates |
| `trace-completion` | Multi-stage trace completion | Start-gated chained SLA stages, delayed-in-success-rate |
| `notification-channels` | Notification channels | Email (SMTP), webhook, Slack, PagerDuty; per-rule channel binding; delivery ledger |
| `notification-profiles` | Notification profiles | Per-integration recipient routing, org default fallback |
| `error-feed` | Errors feed & acknowledgements | Unacknowledged-error tracking per service with a periodic error notifier |
| `maintenance-windows` | Maintenance windows | Scheduled alert suppression with an announcement strip |
| `announcements` | Announcements | Org and cell-wide banners with per-user dismissal |
| `status-badges` | Public status badges | Shareable health badges that leak nothing beyond the badge |
| `digest` | Digest emails | Periodic summary including shared-resource notifications |

### Business lens

| Slug | Feature | What it is |
|---|---|---|
| `messages` | Message views | Business-level message lens over traces; saved shared views |
| `stuck-messages` | Stuck messages | Cross-integration stuck/failed message triage |

### Organisation & access (core)

| Slug | Feature | What it is |
|---|---|---|
| `multi-tenancy` | Multi-tenancy | Hard Postgres / logical ClickHouse isolation per org; operator surface for org lifecycle |
| `roles` | Roles | Admin / editor / viewer at org level; deny-by-default telemetry visibility for non-admins |
| `groups` | Groups (teams) | Group membership as the visibility grant vehicle; attach groups to integrations/systems (viewer) |
| `service-accounts` | Service accounts | Machine identities with own role and tokens; **scoped by default** — visibility via group membership; org-wide read is an explicit, audited opt-in with a cell-wide forbid knob |
| `personal-tokens` | Personal access tokens | Per-user API tokens with role caps and expiry |
| `mfa` | Two-factor auth (TOTP) | Per-user MFA enrolment (the org-wide *requirement* is Enterprise) |
| `demo-accounts` | Demo accounts | Read-only demo users blocked from mutations |
| `config-transfer` | Config export/import | Move org configuration between environments; strict/replace modes, dry-run, atomic |

### Platform & developer surface

| Slug | Feature | What it is |
|---|---|---|
| `api` | REST API | Full JSON API with OpenAPI spec, llms.txt (token-frugal AI format), and a live try-it reference |
| `mcp` | MCP server | Read-only Model Context Protocol tools (HTTP endpoint + stdio binary); inherits the caller token's RBAC |
| `helm-compose` | Deployment | Quickstart compose, Helm chart, single-binary services, embedded migrations |
| `retention` | Telemetry retention | Per-signal retention (free tier up to 14 days; beyond is Enterprise) |
| `cell-settings` | Cell settings | Environment label, ingest base URL, security knobs — operator-gated |

## Enterprise

| Slug | Feature | Entitlement | What it adds |
|---|---|---|---|
| `sso` | SSO / OIDC | `sso` | Per-org OIDC providers, claim → role/group mappings |
| `rbac-advanced` | Advanced RBAC | `rbac_advanced` | Expression-scoped policies, scoped manage (group-editor), resource sharing (viewer-only), per-signal visibility (traces/logs/metrics/messages), team-editor manage for dashboards |
| `audit-log` | Audit log | `audit_log` | Hash-chained, tamper-evident audit trail with UI verification, filters, CSV export, configurable retention |
| `retention-long` | Long retention | `retention_long` | Telemetry retention beyond the free 14-day cap |
| `mfa-policy` | Org-wide MFA policy | `mfa_policy` | Require MFA enrolment for every member (server-side enforcement) |

Licensing note: `max_integrations` (integrations + systems) is an
advisory license field, not a hard gate; there are no seat caps.

## In design (not shipped — do not announce)

| Slug | Feature | Status |
|---|---|---|
| `telemetry-advisor` | Telemetry Advisor (usage-vs-ingest collector suggestions, alert-fatigue advisor) | Design in review — issue #1 |
| `otelflow-integration` | OTelFlow embedded (saved, RBAC-scoped collector configs) | Design in review — issue #3 |
