# integration-monitor-cell

Helm chart for a single Integration Monitor cell — the data-plane stack
that ingests telemetry, stores it, evaluates alert rules, and serves the
UI.

This is the **same chart** that backs a managed SaaS tenant and an
on-premise installation. The differences are configuration, not code:

- `global.mode=saas` runs the cell as part of a managed deployment
  fronted by a control plane.
- `global.mode=onprem` runs the cell standalone with the customer's own
  OIDC issuer.

The chart is **Apache 2.0** so customers may freely fork and modify it
for their environment. The container images it deploys are
FSL-1.1-Apache-2.0; that license governs the running software, not the
deployment glue.

ClickHouse, Prometheus, and Postgres are **not** deployed by this chart.
Point at managed instances or deploy them with their own operators.

## Install

```bash
helm install my-cell ./deploy/helm/cell \
  --set postgres.dsn="postgres://..." \
  --set clickhouse.endpoint="https://clickhouse.example.com" \
  --set prometheus.remoteWriteURL="https://prometheus.example.com/api/v1/write" \
  --set keycloak.issuer="https://auth.example.com/realms/integration-monitor"
```

See [`values.yaml`](./values.yaml) for the full list of values.

## Databases

The chart can either **bundle** Postgres + ClickHouse or connect to ones **you
provide** — pick per environment.

### Turnkey — bundle the databases (quickest)

For evaluation and small installs, let the chart deploy Postgres + ClickHouse
too (single-replica, one PVC each). One command, no external databases:

```bash
helm install my-cell ./deploy/helm/cell -f deploy/helm/cell/values-bundled.yaml
```

Set real passwords in [`values-bundled.yaml`](./values-bundled.yaml). It's not
HA — for production, use external/managed databases (below).

### Bring your own (recommended for production)

You already run Postgres + ClickHouse (managed or via operators — the idiomatic
Kubernetes pattern). Copy [`values-external-db.yaml`](./values-external-db.yaml),
fill in your DSN / endpoint / credentials:

```bash
helm install my-cell ./deploy/helm/cell -f deploy/helm/cell/values-external-db.yaml
```

Don't have them on the cluster yet but want them managed properly? Deploy them
with their own charts/operators, then point the values file at the in-cluster
services:

```bash
# Postgres (Bitnami chart)
helm install pg oci://registry-1.docker.io/bitnamicharts/postgresql --set auth.database=controlplane
# → dsn: postgres://postgres:<pw>@pg-postgresql:5432/controlplane?sslmode=disable

# ClickHouse (Altinity operator, then a ClickHouseInstallation)
kubectl apply -f https://raw.githubusercontent.com/Altinity/clickhouse-operator/master/deploy/operator/clickhouse-operator-install-bundle.yaml
# → endpoint: <chi-service>:9000
```

Either way, cell-api applies its Postgres migrations and creates its ClickHouse
tables on startup, so the databases just need to exist and be reachable.

For a **single-server, no-Kubernetes** setup, the Docker Compose packages under
[`deploy/server/`](../../server/) (and the one-command
[`deploy/quickstart/`](../../quickstart/)) offer the same bundled-or-external
choice.
