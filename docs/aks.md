<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->
# Running a Sluicio cell on AKS

AKS is the lowest-friction home for a full cell: the repo's first-class
deploy artifact is the Helm chart at `deploy/helm/cell`, and ClickHouse gets
real **block storage** (Azure Disk) + StatefulSet semantics via the
clickhouse-operator. Manifests referenced here live in
[`deploy/azure/aks/`](../deploy/azure/aks).

## Topology
| Piece | How | Notes |
|---|---|---|
| **cell-api** | Helm chart Deployment, **1 replica** | API + background evaluators (in-process, no leader election). |
| **cell-ingest** | Helm chart Deployment, scalable | OTLP/HTTP `:4318`. |
| **cell-alerting** | **0 replicas** | No-op scaffold today; cell-api owns the loops. |
| **ClickHouse** | clickhouse-operator + Azure Disk PVC | Logs + traces. |
| **Postgres** | Azure Database for PostgreSQL (managed) | Config/rules/audit; cell-api auto-migrates. |
| **Frontend** | Azure Static Web Apps (or behind the same ingress) | cell-api image is **API-only** — no embedded UI. |

> **Two correctness rules** (baked into `cell-values.yaml`): keep
> `cellApi.replicaCount: 1` (the evaluators have no leader election → 2 pods
> double-fire alerts), and `cellAlerting.replicaCount: 0` (it does nothing
> yet). Scale `cell-ingest` for throughput.

## Steps

### 0. Cluster + tooling
```bash
az aks create -g <rg> -n sluicio-aks --node-count 3 --node-vm-size Standard_D4s_v5 --attach-acr <acr>
az aks get-credentials -g <rg> -n sluicio-aks
helm repo add ingress-nginx https://kubernetes.github.io/ingress-nginx
helm repo add jetstack https://charts.jetstack.io
helm repo add clickhouse-operator https://docs.altinity.com/clickhouse-operator/
```
`--attach-acr` grants the cluster AcrPull so it can pull your images.

### 1. Build + push images (context = repo root)
```bash
az acr build -r <acr> -f services/cell-api/Dockerfile    -t cell-api:<tag>    .
az acr build -r <acr> -f services/cell-ingest/Dockerfile -t cell-ingest:<tag> .
```

### 2. ClickHouse (operator + block storage)
```bash
kubectl apply -f https://raw.githubusercontent.com/Altinity/clickhouse-operator/master/deploy/operator/clickhouse-operator-install-bundle.yaml
kubectl apply -f deploy/azure/aks/storageclass.yaml
kubectl apply -f deploy/azure/aks/clickhouse.yaml      # creates ns 'sluicio', the CH install, NetworkPolicy
kubectl -n sluicio rollout status statefulset.apps -l clickhouse.altinity.com/chi=sluicio
```
This runs single-node ClickHouse on a Premium SSD PVC, default user / no
password, reachable only inside the cluster (NetworkPolicy). The `telemetry`
database is created on init.

### 3. Postgres (managed)
Create an Azure Database for PostgreSQL Flexible Server + a `sluicio`
database, allow access from the AKS subnet (or use a private endpoint), and
build the DSN for the next step.

### 4. Deploy the cell
Edit `deploy/azure/aks/cell-values.yaml` (`<acr>`, `<tag>`, Postgres host +
password), then:
```bash
helm upgrade --install sluicio deploy/helm/cell -n sluicio -f deploy/azure/aks/cell-values.yaml
kubectl -n sluicio rollout status deploy/sluicio-cell-api
```
cell-api runs its Postgres migrations on first boot.

### 5. Ingress + TLS
Install ingress-nginx + cert-manager, create a `letsencrypt` ClusterIssuer,
point DNS at the ingress IP, then:
```bash
kubectl apply -f deploy/azure/aks/ingress.yaml   # edit the hostnames first
```

### 6. Frontend + telemetry
- Host `frontend/` on Azure Static Web Apps; proxy `/api` → `app.example.com`
  (the cell-api ingress host). (Or add a frontend build stage + `go:embed` to
  the cell-api image to serve the SPA itself.)
- Point your OTel Collectors' OTLP/HTTP exporter at `https://ingest.example.com`.
- Sign in at `https://app.example.com` (native admin on first boot).

## Hardening: ClickHouse auth
The simple path above relies on network isolation instead of a ClickHouse
password (the cell chart injects only `CLICKHOUSE_ENDPOINT`). To add real CH
auth: create a CH user with a password in the `ClickHouseInstallation`, then
inject `CLICKHOUSE_USERNAME` / `CLICKHOUSE_PASSWORD` (and `CLICKHOUSE_DATABASE`)
into the cell-api **and** cell-ingest pods. The chart doesn't template those
yet, so add them to `deploy/helm/cell/templates/cell-api.yaml` and
`cell-ingest.yaml` `env:` from a Secret, e.g.:
```yaml
- name: CLICKHOUSE_USERNAME
  value: {{ .Values.clickhouse.username | quote }}
- name: CLICKHOUSE_PASSWORD
  valueFrom: { secretKeyRef: { name: clickhouse-credentials, key: password } }
- name: CLICKHOUSE_DATABASE
  value: {{ .Values.clickhouse.database | quote }}
```
(The app already reads those env vars via `pkg/clickhouse` `ConfigFromEnv`.)

## Verify
```bash
kubectl -n sluicio get pods
kubectl -n sluicio logs deploy/sluicio-cell-api | grep -i migrat
# CH reachable + telemetry DB present:
kubectl -n sluicio exec -it chi-sluicio-main-0-0-0 -- clickhouse-client -q "SHOW DATABASES"
```
Then create a log/metric alert rule bound to a service, send some telemetry,
and confirm it fires + delivers to a channel.
