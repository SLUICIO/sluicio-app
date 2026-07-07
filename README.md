<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Sluicio

[![CI](https://github.com/SLUICIO/sluicio-app/actions/workflows/ci.yml/badge.svg)](https://github.com/SLUICIO/sluicio-app/actions/workflows/ci.yml)
[![Release images](https://github.com/SLUICIO/sluicio-app/actions/workflows/release-images.yml/badge.svg)](https://github.com/SLUICIO/sluicio-app/actions/workflows/release-images.yml)
[![Latest release](https://img.shields.io/github/v/release/SLUICIO/sluicio-app?sort=semver&display_name=tag&label=release)](https://github.com/SLUICIO/sluicio-app/releases)
[![License: FSL-1.1-Apache-2.0](https://img.shields.io/badge/license-FSL--1.1--Apache--2.0-blue)](LICENSE)

**Integration monitoring built on OpenTelemetry.** Sluicio gives teams who run
heterogeneous integration estates — Apache Camel, Azure Functions and Logic Apps,
ActiveMQ Artemis, RabbitMQ, Kafka, OTel Collectors, custom microservices — a
single place to model, observe, and alert on the **integrations** those
services collectively make up, not just the services in isolation.

It is a SaaS *and* a self-hostable platform, built from one codebase. Point an
OpenTelemetry Collector at it and Sluicio discovers your services from their
telemetry, lets you group them into the business integrations they serve, and
turns starter health checks on with a click.

## Try it live

A hosted demo runs at **[demo.sluicio.com](https://demo.sluicio.com)** — log in
and click around a Sluicio pre-loaded with example integrations and live
telemetry. It resets periodically, so explore freely; nothing you do sticks.

```
email:     demo@sluicio.com
password:  demodemo
```

The demo account can read and configure the product, but self-service and
organization administration are disabled — so one visitor can't lock out the
next.

## Quick start

One compose file — no clone, no configuration:

```bash
curl -LO https://raw.githubusercontent.com/SLUICIO/sluicio-app/main/deploy/quickstart/docker-compose.yml
docker compose up -d
```

Open **http://localhost:8080** and sign in as `admin@sluicio.local` / `admin`
(then change the password from the user menu). Send it telemetry (OTLP/HTTP)
at `http://localhost:4318`. That's the free **Community edition** — unlimited
integrations, users, and data. Details and production options (single-server,
Kubernetes, bring-your-own databases) are in
[`deploy/`](deploy/) → [quickstart](deploy/quickstart/) ·
[server](deploy/server/) · [Helm](deploy/helm/cell/).

---

## Why Sluicio

Most observability tools are service-centric: they show you that
`payment-service` is slow. Integration teams think in **flows** — "the partner
EDI intake is failing" — which span a queue, three services, and a scheduled
job. Sluicio is organised around that reality:

- **Integrations as first-class objects.** Compose services, queues, and jobs
  into an integration and monitor its end-to-end health, not just its parts.
- **Vendor-neutral by construction.** Everything rides on OpenTelemetry
  (OTLP). No proprietary agent — if it emits OTLP traces, logs, or metrics,
  Sluicio understands it.
- **Cost-aware.** Telemetry is expensive. Sluicio helps you keep only the
  signals you act on, with built-in *trim-ingestion* advisors that generate
  Collector config to drop the rest.
- **Self-host or SaaS, same code.** Run it in your own cluster or use the
  managed offering — identical features, identical bits.

## Features

### Model your estate
- **Service auto-discovery** from incoming OTLP — last-seen, span/error counts,
  latency (p50/p95), and a live status pill, with no manual registration.
- **Integrations** that group services, queues, and jobs into the business
  flow they serve, with their own health, logs, errors, and messages views.
- **System identification** — Sluicio recognises the technology behind a
  service (RabbitMQ, Kafka, ActiveMQ/Artemis, Redis, SQL Server, Postgres,
  MongoDB, Elasticsearch, OTel Collector, …) from its emitted metrics; the
  list of kinds is user-extensible.
- **Catalog & metadata** — service types, tags, schemas, metadata fields, and
  service facets to organise and search a large estate.
- **Topology & maps** — visualise dependencies and lay out integration maps.

### Health checks & alerting
- **Metric, log, and trace health checks** with thresholds, evaluation
  windows, and aggregations (max / min / avg / sum / p95 / last / increase /
  rate), scopable to a service or an integration.
- **Alert rules → notification channels**, wired at apply time so a check
  starts paging the right place immediately.
- **Monitoring templates** — a built-in catalog of starter checks per system
  kind, **auto-detected** from a service's metrics and applied (or removed)
  with one click. Save your own checks as **custom templates**, fork them, and
  reuse across services.
- **"In trouble" overview** — the Errors page rolls failures up by integration
  and by system, with drill-down to the specific failing checks (RBAC-aware).

### Explore telemetry
- **Metrics explorer** — search the full metric catalog, chart any metric over
  a window, filter by attributes, group by service/integration/type/attribute,
  and build an alert from the same view. Available globally and scoped to a
  single service.
- **Logs explorer** with severity and attribute filtering.
- **Trace search & waterfall** across span names, attribute values, and error
  messages.
- **Stuck / queued messages** view for messaging integrations.

### Operate & keep costs down
- **Dashboards** — auto-generated per service plus pinnable system-health cards.
- **"Since your last visit" digest** — new services, freshly detected
  collectors to set up, and integrations that started failing, filtered to what
  you're allowed to see.
- **Trim-ingestion advisors** for metrics, logs, and traces — generate an OTel
  Collector config that drops the signals you never query.
- **RBAC** via group access policies (by service, integration, attribute,
  compound, system, or whole-org).
- **Multi-tenancy** — every resource belongs to an org; users can span orgs
  with different roles.
- **Native auth out of the box** (argon2id + HTTP-only session cookies), with
  optional per-org **OIDC SSO** (Entra, Okta, Google Workspace, Keycloak — any
  OIDC-conformant IdP).

## Architecture

One codebase, two deployment shapes:

- A shared **control plane** (`controlplane`) — authentication, organizations,
  users, invitations, billing, and the registry of which tenants live in which
  cell.
- A **cell** (data plane) that ingests OTLP, stores telemetry, evaluates rules,
  and serves the UI + API. The same cell that backs a managed tenant is what an
  on-prem customer deploys into their own cluster.

A cell is made of four Go services:

| Service | Role |
| --- | --- |
| `cell-ingest` | OTLP receiver → writes traces, logs, and metrics to ClickHouse |
| `cell-api` | Cell-local API + the React UI: services, integrations, rules, queries |
| `cell-alerting` | Polling rule engine + notification dispatch |
| `cell-controller` | Provisions and lifecycles cells in Kubernetes |

**Data stores:** Postgres holds the control-plane state (orgs, users, sessions,
rules, metadata); ClickHouse (`telemetry` database) holds the OTLP traces,
logs, and metrics. The local dev stack also runs Prometheus.

**Frontend:** React + TypeScript (Vite), served by `cell-api`.

See [`docs/architecture.md`](docs/architecture.md) for the full design and
[`docs/decisions.md`](docs/decisions.md) for the decisions behind it.

## Repository layout

```
.
├── services/                Go services (FSL-1.1-Apache-2.0)
│   ├── controlplane/        Shared control plane (orgs, users, cell registry)
│   ├── cell-api/            Cell-local API + UI: integrations, rules, queries
│   ├── cell-alerting/       Polling rule engine + notification dispatch
│   ├── cell-ingest/         OTLP receiver -> ClickHouse
│   └── cell-controller/     Provisions and lifecycles cells in Kubernetes
├── pkg/                     Internal Go libraries inc. license verification (FSL-1.1-Apache-2.0)
├── plugins/                 Plugin contracts for third parties (Apache-2.0)
├── ee/                      Proprietary bits only — audit-log store + license-mint tool (ee/LICENSE.md)
├── frontend/                React + TypeScript UI (FSL-1.1-Apache-2.0)
├── deploy/
│   ├── dev/                 Local dev stack config (Prometheus, etc.)
│   ├── server/              Single-server production deploy (compose + Caddy + systemd)
│   ├── helm/                Helm charts for control plane and cell (Kubernetes)
│   └── otel-collector/      Example collector configs (Apache-2.0)
├── docs/                    Architecture, decisions, licensing (Apache-2.0)
├── docker-compose.yml       Local development environment
├── Makefile                 Common dev tasks (run `make help`)
├── CHANGELOG.md             Internal changelog (generated; not shown in product)
├── LICENSE                  Top-level licensing summary
├── LICENSE-FSL              Functional Source License v1.1 (Apache 2.0 future)
├── LICENSE-APACHE           Apache License 2.0
└── NOTICE                   Per-directory license mapping
```

## Quick start (local development)

Prerequisites: Go 1.22+, Node 20+, and Podman or Docker.

```bash
# 1. bring up the full stack (Postgres, ClickHouse, Prometheus, cell-api,
#    cell-ingest). Builds the app images on first run.
make dev-up

# 2. seed some synthetic traces, logs, and metrics so the UI has data
make seed-traces
# …or stream them continuously:
make seed-traces-loop

# 3. (optional) run the frontend dev server with hot reload
make frontend-dev          # http://localhost:5173
```

`cell-api` (with the built UI) comes up on its mapped port from `make dev-up`;
use `make frontend-dev` only when you're iterating on the UI. On first boot the
cell-api seeds a **Default** org and an admin user — sign in with
`admin@sluicio.local` / `admin` and change the password.

After a code change, rebuild just the app containers (data stores keep their
data):

```bash
make dev-rebuild
make dev-logs              # tail logs    | make dev-ps — status | make dev-down — stop
```

In real use, replace `seed-traces` with an OpenTelemetry Collector pointed at
the ingest endpoint (`/v1/traces`, `/v1/logs`, `/v1/metrics`). An example
config lives at
[`deploy/otel-collector/push-to-cell.yaml`](deploy/otel-collector/push-to-cell.yaml).
Run `make help` for the full task list.

## Deployment

- **Single server** — [`deploy/server/`](deploy/server/) runs the stack with
  compose behind Caddy as a systemd service, with Postgres backups. CI
  (`.github/workflows/release-images.yml`) builds and publishes the images to
  `ghcr.io/<owner>` on every push to `main` (`:latest` + `:<sha>`) and on each
  `vX.Y.Z` tag (the versioned release); cut one with `scripts/release.sh vX.Y.Z`.
- **Kubernetes** — Helm charts for the control plane and the cell live under
  [`deploy/helm/`](deploy/helm/).
- **Images** — `make docker-build` / `make docker-push` build and push all
  service + frontend images, version-stamped from the git tag.

## Versioning

Versions are SemVer derived from git tags (`git describe --tags`) and stamped
into every binary and the UI footer at build time. Cut a release with
`scripts/release.sh vX.Y.Z` (refreshes [`CHANGELOG.md`](CHANGELOG.md), commits,
and tags); `make version` prints the current build version.

## Licensing

The product is **FSL-1.1-Apache-2.0**, peripheral code that third parties
build on (plugin contracts, cell Helm chart, OTel config, docs, e2e suite) is
**Apache-2.0**, and the `ee/` directory is licensed separately under the
**Sluicio Enterprise License** ([`ee/LICENSE.md`](ee/LICENSE.md)). The
per-directory map lives in [`NOTICE`](NOTICE).

The open-core boundary is deliberately narrow. The Enterprise *features* —
SSO, advanced RBAC, audit logging, long retention, MFA enforcement — and the
license **verification** itself live in the open core (FSL); they're fully
auditable and simply **gated by a license key at runtime**. Only two things
are proprietary in `ee/`: the **audit-log persistence** and the **license
mint tool** (which needs a private signing key that never ships). So you can
read exactly how licensing works and run every line of the product; you just
can't *issue* yourself a license. The core builds and runs with **no
Enterprise code present at all** — `ee/` is wired in only at the service's
composition root.

In one paragraph: the product — services, frontend, control-plane Helm chart,
internal shared libraries — is licensed under the **Functional Source License
v1.1** with an Apache 2.0 future grant. Anyone may read, modify, self-host, and
audit the code; only competing-as-a-service is restricted, and each release
becomes Apache 2.0 automatically two years after publication. Peripheral code
that third parties build on — plugin contracts, the cell Helm chart, OTel
collector configs, docs, and SDK helpers — is **Apache License 2.0** from the
first commit. See [`docs/licensing.md`](docs/licensing.md) for the rationale
and [`NOTICE`](NOTICE) for the per-directory mapping.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for how to build, test, and propose
changes, and [`SECURITY.md`](SECURITY.md) to report a vulnerability privately.
