# Architectural decisions

This document captures the decisions made during the planning conversation
that produced this repository. Each entry records *what* was decided and
*why*, so a later reader (or a later us) doesn't have to reconstruct the
reasoning. New decisions are appended; superseded decisions are kept and
struck through.

## D-001: Product positioning

The product is a monitoring platform specialized for **system
integrations** — for example BizTalk, Azure Functions, Azure Logic Apps,
ActiveMQ Artemis, partner integrations, ETL flows — rather than a generic
infrastructure or APM tool. The differentiating value is that telemetry is
modeled and presented per *integration scope* (a logical grouping of
services), not per service or per host.

The target customer runs a heterogeneous integration estate and lacks a
unified place to monitor it. Existing tools either focus on infrastructure
(Datadog, New Relic) or are deeply tied to one vendor's ecosystem (Azure
Monitor for Logic Apps, BizTalk360).

## D-002: OpenTelemetry as the substrate

All telemetry — traces, metrics, logs — flows through OpenTelemetry data
models and protocols. Customers are expected to instrument their services
with OTel or use OTel-compatible exporters. This minimizes the surface area
of vendor-specific adapters and gives the product a single internal data
model.

## D-003: Two deployment shapes from one codebase

The product runs as SaaS first, with paying customers able to also run it
on-premise on Kubernetes. To avoid maintaining two architectures, both
deployments share the same codebase and Helm chart for the data plane (the
"cell" — see D-005). Only the SaaS adds a shared control plane on top.

## D-004: Hybrid cell-based multi-tenancy

Multi-tenancy is implemented with a **hybrid cell architecture**:

- A shared **control plane** holds the global directory: organizations,
  users, memberships, invitations, billing, and the mapping of which
  tenants live in which cells. The control plane stores its data in
  Postgres.
- Each **cell** is an isolated data-plane stack containing ClickHouse,
  Prometheus (or Mimir at scale), the OTLP ingest service, the alerting
  engine, the cell API, and a slice of the UI. A cell can be dedicated
  to a single enterprise tenant or shared among smaller tenants.

This avoids the worst case of pure-shared multi-tenancy (tenant isolation
relies on `WHERE tenant_id = ?` queries — a leak risk for healthcare and
finance customers) and pure-dedicated tenancy (an unreasonable
infrastructure bill for free-tier users).

The on-premise product is the same cell Helm chart, just deployed into the
customer's Kubernetes cluster instead of ours.

## D-005: Storage in managed mode

- **Metrics**: Prometheus, graduating to Grafana Mimir when a cell's
  volume exceeds what a single Prometheus instance can handle.
- **Logs and traces**: ClickHouse (Apache 2.0 open-source edition).

ClickHouse was chosen over Loki + Jaeger because it (a) handles
high-cardinality OTel attributes far better than Loki's label-only
indexing, (b) consolidates two storage systems into one for both logs and
traces, (c) compresses heavily, and (d) is the de-facto choice in
modern OTel-native projects (SigNoz, Uptrace, HyperDX).

Operational complexity is acknowledged: clustering ClickHouse is harder
than running plain Prometheus. We mitigate this by starting with a single
beefy node per cell and only sharding when volume demands it.

## D-006: Dual ingestion modes

Customers choose, per data source, between two ingestion modes:

- **Push (managed)**: The customer points their OTel Collector at our
  OTLP endpoint. We store the data in the cell's ClickHouse / Prometheus.
- **Pull (BYO backend)**: The customer keeps their telemetry in their
  existing Prometheus / Loki / ClickHouse / Jaeger / Tempo. We query
  those backends through an adapter layer.

Both modes feed the same internal query model, so the UI and alerting
engine don't care which mode is in use. Adapters needed for v1: native
ClickHouse, Prometheus (PromQL HTTP API), Loki (LogQL HTTP API).

## D-007: No source-specific adapters in v1

The product accepts only OTLP in v1. Bridges for systems that don't emit
OTel natively (BizTalk pipelines, ActiveMQ Artemis JMX, Azure App
Insights) are explicitly **deferred to v2**. These bridges are
high-value-add for the target audience and a likely premium feature, but
each one is real engineering and would slow v1.

## D-008: Integration scope as rule-based tagging

An *integration* is a user-defined entity with matching rules — for
example "services whose `service.name` starts with `brx` belong to
integration `abc`". A service can belong to multiple integrations.
Classification is dynamic (queries filter at read time) so that adding
or changing matchers re-classifies existing telemetry retroactively
without backfilling.

## D-009: Alerting engine — custom, polling-based, cross-signal

The alerting engine is custom, not a re-skin of Prometheus's rule engine.
Rules are structured objects (not free-form PromQL) so the UI can render
authoring cleanly and non-technical operators can write rules. Rules span
all three OTel signals: metrics (e.g. queue depth > 100), traces (e.g.
root-span duration > 8 minutes), and logs (e.g. body matches regex).

Evaluation is polling-based in v1 — every rule runs on a cadence and
queries the configured backend. Streaming evaluation is deferred. This
favors implementation simplicity and predictable resource use over
sub-second latency, which is the right tradeoff for integration
monitoring.

The engine emits alert state transitions (pending → firing → resolved) to
the notification subsystem.

## D-010: Notification plugin architecture

Built-in notification channels for v1: **email, webhook, AMQP, Kafka**.
SMS, Slack, Teams, PagerDuty, and Opsgenie can follow.

Channels are implemented as a Go interface (`Notifier`) and compiled into
the binary in v1. The interface is designed to be exposable later over
gRPC using the HashiCorp `go-plugin` pattern, so third-party plugins
become possible without an interface rewrite.

The plugin **contract** lives in `plugins/` under Apache 2.0 so external
authors can implement against it. Built-in implementations live in
`services/cell-alerting/internal/notifiers/` under FSL.

## D-011: Trust and audit baked into the foundation

Customers will route healthcare claims, invoices, and other regulated
data through their integrations. The platform must be trustworthy from
day one, not retrofitted. Non-negotiable baseline:

- Append-only audit log of every rule change, dispatch, acknowledgement,
  and admin action.
- Durable, retried outbound queue for notifications — never silently drop
  an alert when a channel is down.
- Idempotent alert handling so retries don't double-notify.
- TLS everywhere, with mTLS as an option for on-prem.
- Encryption at rest by default.
- Per-tenant retention controls.

## D-012: Authentication and tenancy model

Authentication is **OIDC** via Keycloak. Keycloak handles SSO federation
to customer IdPs (Azure AD is the dominant case in the target audience)
without us implementing per-IdP integration.

The org / user model:

- A user signs in via OIDC. On first sign-in they are prompted to either
  **create a new organization** or **accept a pending invite** to an
  existing one.
- Org owners can invite users by email; invitations create a pending
  membership that becomes active when the invitee completes OIDC sign-in.
- Roles (v1): `owner`, `admin`, `editor`, `viewer`.

## D-013: V1 UI scope (subject to evolution)

The four views considered non-negotiable for the MVP:

1. **Integration topology** — services in an integration and the message
   flow between them, derived from trace data.
2. **Integration health** — throughput, error rate, latency aggregated at
   the integration level.
3. **Stuck / in-flight messages** — oldest unacknowledged work per
   integration.
4. **Alerts and incidents** — rule list, current alert state, history.

Deferred to v2: dead-letter explorer with replay UI, partner SLA
dashboards, schema/contract drift detection. UI scope is explicitly
expected to shift during development.

## D-014: Visualization is custom, not Grafana

The integration-centric views above don't map cleanly to general-purpose
dashboards. The UI is built as a custom React + TypeScript app rather than
an embedded Grafana, even though that costs more to build.

## D-015: Technology stack

- **Backend**: Go (chosen because Prometheus, Mimir, Jaeger, Tempo,
  Loki, and the OTel Collector are all Go and ship reusable client
  libraries; single static binaries are ideal for the on-prem Helm
  chart; concurrency model fits multi-backend query fan-out).
- **Control plane database**: Postgres.
- **Frontend**: React + TypeScript, Vite.
- **Auth**: Keycloak (OIDC).
- **Deployment**: Helm chart on Kubernetes, the same artifact for SaaS
  and on-prem.

## D-017: Migrate ingest to OTel Collector + thin gateway (deferred)

The v0.x ingest path is custom Go code in `cell-ingest`: an OTLP/HTTP
receiver that decodes protobuf, converts spans, and writes to
ClickHouse directly. This is enough to iterate on product UX, but it
falls short of production-grade observability ingest on several axes:

- No OTLP/gRPC support (default transport for many SDKs).
- No OTLP/JSON support.
- No batching, memory limiting, retry-with-backoff, or partial-success
  responses on the exporter side.
- Logs and metrics would re-do the proto-decode-and-insert work.
- No path for non-OTLP source adapters without writing more services.

The agreed architecture for ingest is **a thin gateway in front of an
OpenTelemetry Collector**, not "Collector instead of cell-ingest":

- `cell-gateway` (a slimmed-down `cell-ingest`) handles tenant
  authentication, rate limiting, quota enforcement, audit logging,
  and stamping `im.tenant_id` onto each batch. It then forwards the
  unchanged OTLP batch downstream.
- An OTel Collector instance runs alongside the gateway in each cell.
  It handles batching, memory limiting, retries, and writes traces,
  logs, and metrics via the contrib `clickhouseexporter` and Prometheus
  remote-write.

This split keeps multi-tenancy, audit, and trust properties in code we
fully control, while the Collector handles the OTel-protocol and
storage heavy lifting. It also turns future source adapters (BizTalk,
Artemis, App Insights) into Collector receivers rather than new
microservices.

**Status: deferred.** We continue product development on the current
`cell-ingest`. The refactor is to land **before**:

- Exposing the platform to a real customer.
- Implementing logs or metrics ingestion.
- Adding source-specific adapters (D-007's deferred items).

Migration plan when we do it:
1. Add the Collector to the cell Helm chart, with its OTLP receivers
   and ClickHouse exporter pointed at the existing `traces` table
   (after a one-time schema reconciliation against the exporter's
   conventions).
2. Rewrite `cell-ingest` as `cell-gateway`: keep the OTLP receiver,
   drop the ClickHouse writer, add OTLP forwarder.
3. Move trace-schema ownership into the Collector exporter config.
   Keep migration tooling for our own app tables (alert rules,
   integrations, audit log, etc.).
4. Add OTLP/gRPC pass-through to the gateway.

## D-016: Licensing

The product (`services/`, `pkg/`, `frontend/`, `deploy/helm/controlplane/`)
is licensed under **Functional Source License v1.1** with an Apache 2.0
future grant (FSL-1.1-Apache-2.0). The conversion date is two years from
each version's publication.

Periphery — `plugins/`, `deploy/helm/cell/`, `deploy/otel-collector/`,
`docs/`, `sdk/` — is licensed under **Apache 2.0** from the first commit.

See [`licensing.md`](licensing.md) for the rationale.
