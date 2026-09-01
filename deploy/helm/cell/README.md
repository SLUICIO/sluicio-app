# sluicio-cell

Helm chart for a complete self-hosted **Sluicio cell**: the UI, the API
(`cell-api`), the OTLP ingest endpoint (`cell-ingest`), and — optionally —
bundled Postgres + ClickHouse.

This is the **same chart** for Community and Enterprise, and for on-premise
and managed SaaS cells. Enterprise features (SSO, advanced RBAC, audit log,
long retention, MFA policy) are enabled **at runtime by the license key** —
no separate chart, images, or reinstall (see [License](#license-enterprise)).
The control plane is SaaS-only and closed source; a self-hosted cell never
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
but lands in the release Secret.

**Community Edition is the absence of a license** — there is no edition flag.
Leave `license.existingSecret` and `license.key` empty and the chart doesn't
render `SLUICIO_LICENSE_KEY` at all; everything except SSO, audit log,
notification profiles, retention beyond 14 days, the require-MFA policy and
advanced RBAC works. Adding a license later is a `helm upgrade`, not a
reinstall — the databases are untouched.

Note that a **non-empty `existingSecret` naming a Secret that doesn't exist**
is worse than leaving it empty: the pod won't start (see Troubleshooting).

### Renewing without a rollout

An environment variable is read once, when the container starts, so updating
the Secret does nothing until the pod restarts. That is fine for a perpetual
license and tiresome for one issued per contract period: a quarterly
agreement means four renewals a year, each one a rollout of a Deployment
that must stay single-replica.

```yaml
license:
  existingSecret: sluicio-license
  mountAsFile: true
```

The token is then mounted rather than injected, and renewing is:

```bash
kubectl create secret generic sluicio-license   --from-literal=license=<new-token> --dry-run=client -o yaml | kubectl apply -f -
```

The kubelet refreshes the mounted Secret (up to about a minute) and cell-api
re-reads the file on `license.reloadInterval`. Nothing restarts, no
notification is lost, no alert evaluation is missed. The mount deliberately
avoids `subPath`, which is copied once and never refreshed.

Off by default because the two forms fail differently. A Secret key that
does not exist makes an env-var reference fail the pod outright, which is
impossible to miss; mounted, the file is simply absent and the cell runs
unlicensed with a warning in its log. Every other failure keeps the license
already in force - see [license renewal](../../../docs/license-renewal.md).

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
each — not HA, so external databases stay the production recommendation. They
run on OpenShift too (see below); `values-openshift.yaml` handles the one
setting that needs to differ.

## OpenShift

`values-openshift.yaml` is a working starting point:

- **Routes** instead of Ingress (`route.enabled`) — two Routes share the UI
  hostname (`/` and `/api`), one more serves OTLP ingest.
- The application pods (cell-api, cell-ingest, frontend) run under the
  **restricted / restricted-v2 SCC unchanged**: the Go images are distroless
  non-root, and the chart runs the frontend's nginx on an unprivileged port
  with chart-provided writable dirs — any assigned UID works.
- The **bundled databases also run under restricted-v2** — `values-openshift.yaml`
  nulls their `podSecurityContext`, and that's the whole trick. The chart pins
  `fsGroup` 70/101 for plain Kubernetes; the restricted SCC allocates fsGroup
  from the namespace's range and rejects a pin outside it. Drop the pin and
  OpenShift assigns one, the PVC is group-owned, and the stock images run
  fine. **`anyuid` is not required.** Flip `postgres.enabled` /
  `clickhouse.enabled` on and nothing else is needed.
- External databases remain the **production** recommendation on OpenShift as
  everywhere else — not for SCC reasons, but because the bundled ones are
  single-replica with a PVC each.

## Troubleshooting

**The UI loads but says "Couldn't reach the cell-api", 502 Bad Gateway.**
The frontend proxies `/api` to the cell-api Service; a 502 means nothing
healthy is behind it. cell-api only becomes Ready once it has connected to
*both* Postgres and ClickHouse, so a 502 is nearly always cell-api not being
Ready rather than a proxy problem:

```bash
kubectl get pods -l app.kubernetes.io/instance=<release>
kubectl logs deploy/<release>-cell-api
```

**cell-api is `CreateContainerConfigError`.** kubelet couldn't assemble the
container config — a referenced Secret is missing, or the key inside it isn't
the one the chart asks for. The pod events name it exactly:

```bash
kubectl describe pod -l app.kubernetes.io/component=cell-api
```

`secret "X" not found` → the Secret isn't in this namespace (easy to hit when
you create it in one project and install into another). `couldn't find key Y
in Secret X` → the Secret exists with a different key; either recreate it or
point the chart at your key with `license.secretKey` / `mfa.secretKey` /
`smtp.secretKey`. The defaults are `license`, `mfa-key` and `password`.

A quick way back to a running install is to drop the references entirely —
Community Edition, no MFA enrollment, everything else working:

```bash
helm upgrade <release> <chart> --reuse-values \
  --set license.existingSecret= --set license.key= \
  --set mfa.existingSecret= --set mfa.key=
```

Note that cell-ingest has no such Secret references, so "ingest is Running but
cell-api isn't" is the signature of this problem rather than of an image,
SCC or scheduling issue.

**Pods stuck `CreateContainerConfigError` on OpenShift with no Secret in the
events** is the other flavour: an image whose `USER` isn't numeric can't be
verified against `runAsNonRoot`. The Sluicio images pin a numeric UID, so this
points at the bundled database images, not the app.

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
