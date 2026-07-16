# sluicio-cell

Helm chart for a complete self-hosted **Sluicio cell**: the UI, the API
(`cell-api`), the OTLP ingest endpoint (`cell-ingest`), and — optionally —
bundled Postgres + ClickHouse.

This is the **same chart** for Community and Enterprise, and for on-premise
and managed SaaS cells. Enterprise features (SSO, advanced RBAC, audit log,
long retention, MFA policy) are enabled **at runtime by the license key** —
no separate chart, images, or reinstall (see [License](#license-enterprise)).
The separate `controlplane` chart is SaaS-only; a self-hosted cell never
needs it.

The chart is **Apache 2.0** so customers may freely fork and modify it for
their environment. The container images it deploys are FSL-1.1-Apache-2.0;
that license governs the running software, not the deployment glue. The
official images are **public** on `ghcr.io/sluicio` — no pull secrets needed
(only set `global.imagePullSecrets` if you mirror into a private registry).

## Quick start

```bash
# Turnkey (chart-managed Postgres + ClickHouse; eval / small installs):
helm install sluicio ./deploy/helm/cell -f deploy/helm/cell/values-bundled.yaml

# Production (your own databases):
helm install sluicio ./deploy/helm/cell -f my-values.yaml
```

where `my-values.yaml` starts from
[`values-external-db.yaml`](./values-external-db.yaml) and adds your ingress
host + license (or [`values-openshift.yaml`](./values-openshift.yaml) on
OpenShift).

First run: open the UI — the setup flow creates the first admin and org.
`cell-api` applies database migrations automatically on startup.

## Exposure — one hostname for UI + API

The cell-api **session cookie is host-scoped**, so the UI and `/api` must be
served from the same hostname. The chart's `ingress` does this for you
(`/` → frontend, `/api` → cell-api) plus a second hostname for OTLP ingest:

```yaml
ingress:
  enabled: true
  className: nginx
  host: sluicio.acme.com            # UI + /api
  tls: [{ secretName: sluicio-tls, hosts: [sluicio.acme.com] }]
  ingest:
    enabled: true
    host: ingest.acme.com           # collectors POST OTLP/HTTP here
    tls: [{ secretName: ingest-tls, hosts: [ingest.acme.com] }]
```

`SLUICIO_APP_URL` / `SLUICIO_INGEST_URL` (email deep links, SSO redirect
base, the advertised ingest origin) default to these hosts; override via
`app.appUrl` / `app.ingestUrl` if they differ.

On OpenShift use `route.enabled: true` instead (see below).

## License (Enterprise)

```bash
kubectl create secret generic sluicio-license --from-literal=license=<token>
```

```yaml
license:
  existingSecret: sluicio-license
```

`helm upgrade` after adding it — entitlements activate immediately; verify
with `GET https://<host>/api/v1/license`. An inline `license.key` also works
but lands in the release Secret. Unlicensed = Community features, fully
supported.

Two more keys worth setting on day one:

```yaml
mfa:
  key: "<openssl rand -base64 32>"   # or existingSecret — enables MFA enrollment
smtp:
  host: smtp.acme.com                # invitations, password resets, alert emails
  from: sluicio@acme.com
  existingSecret: sluicio-smtp       # key `password`
```

## Databases

### Bring your own (recommended for production)

```yaml
postgres:
  dsn: "postgres://user:pass@your-pg:5432/controlplane?sslmode=require"
clickhouse:
  endpoint: "your-ch:9000"           # native protocol
  username: sluicio
  passwordSecret: clickhouse-credentials   # Secret with key `password`
```

Deploy them with their own charts/operators if needed:

```bash
# Postgres (Bitnami chart)
helm install pg oci://registry-1.docker.io/bitnamicharts/postgresql --set auth.database=controlplane
# ClickHouse (Altinity operator, then a ClickHouseInstallation)
kubectl apply -f https://raw.githubusercontent.com/Altinity/clickhouse-operator/master/deploy/operator/clickhouse-operator-install-bundle.yaml
```

### Bundled (eval / small installs)

`values-bundled.yaml` deploys single-replica Postgres + ClickHouse with a PVC
each — not HA, and on OpenShift prefer external databases (the stock images
want fixed UIDs; see below).

## OpenShift

`values-openshift.yaml` is a working starting point:

- **Routes** instead of Ingress (`route.enabled`) — two Routes share the UI
  hostname (`/` and `/api`), one more serves OTLP ingest.
- The application pods (cell-api, cell-ingest, frontend) run under the
  **restricted / restricted-v2 SCC unchanged**: the Go images are distroless
  non-root, and the chart runs the frontend's nginx on an unprivileged port
  with chart-provided writable dirs — any assigned UID works.
- The **bundled databases are the exception**: the stock postgres/clickhouse
  images assume fixed UIDs. Use external databases on OpenShift (recommended),
  or grant those pods `anyuid` and set
  `postgres.podSecurityContext: {}` / `clickhouse.podSecurityContext: {}`.

## Topology notes

- **`cellApi.replicaCount` must stay 1** — cell-api runs in-process
  schedulers (alert evaluation, notification delivery) and applies DB
  migrations on startup. The Deployment uses the `Recreate` strategy so
  upgrades never run two instances. `cell-ingest` and `frontend` scale
  freely.
- `cell-alerting` is deliberately **not** deployed: cell-api owns the alert
  loops today (same as the Compose packages).
- The remote MCP endpoint (`/api/v1/mcp`) and the API docs (`/api/docs`) are
  served by cell-api — they ride the same `/api` route, no extra service.
- Extra env (e.g. `SLUICIO_AUDIT_SINK_URL`, `ERROR_NOTIFY_INTERVAL`):
  `cellApi.extraEnv` / `cellIngest.extraEnv`.

For a **single-server, no-Kubernetes** setup, the Docker Compose packages
under [`deploy/server/`](../../server/) (and the one-command
[`deploy/quickstart/`](../../quickstart/)) offer the same bundled-or-external
choice.
