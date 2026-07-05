<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->
# Running a Sluicio cell on Azure Container Apps (ACA)

A Sluicio **cell** is three stateless Go services plus three stateful
dependencies. ACA is a great home for the services and the **wrong** home for
the data stores — run those as managed/external services.

| Component | Where | Notes |
|---|---|---|
| **cell-api** | ACA app, external HTTPS ingress `:8081` | API only (see *Frontend* below). Runs the evaluators in-process → **single replica** (see *Gotchas*). |
| **cell-ingest** | ACA app, external HTTPS ingress `:4318` | OTLP/**HTTP** only (the image doesn't expose gRPC). Stateless → autoscales. |
| **Postgres** | Azure Database for PostgreSQL Flexible Server | Config/rules/dispatch/audit. cell-api runs migrations on startup. |
| **ClickHouse** | ClickHouse Cloud (Azure) or ClickHouse on AKS/VM | Logs + traces. The Helm chart also treats CH as external. |
| **Prometheus** | Azure Monitor managed Prometheus (optional) | Only needed for **metric** alerts. Logs/traces and **log-based alerts** work without it. |
| **Frontend (SPA)** | Azure Static Web Apps | The cell-api container image is **API-only** — no UI is embedded. Build `frontend/` and host the `dist/`, proxying `/api` → the cell-api FQDN. |

## Two ACA-specific gotchas (important)

1. **cell-api must be a single replica.** The alert engine, trace-completion
   evaluator, log evaluator, catalog reconciler and retention enforcer all run
   **in-process inside cell-api** with **no leader election** — the code is
   explicit that "running multiple replicas could double-fire". And ACA
   **scale-to-zero would freeze** those background loops. So: `minReplicas =
   maxReplicas = 1` for cell-api. Scale **cell-ingest** for throughput instead.
   *(The repo has a separate single-replica `cell-alerting` service for when
   those loops are extracted; until then, keep cell-api at 1.)*
2. **OTLP is HTTP-only here.** cell-ingest exposes `:4318` (OTLP/HTTP) and does
   not serve gRPC, so ACA's single-port HTTP ingress is sufficient — point your
   OpenTelemetry Collectors' OTLP/HTTP exporter at the cell-ingest FQDN.

## Configuration (env vars)

Set via ACA env vars; put DSN/passwords in **ACA secrets** (`secretRef`):

- **cell-api**: `CELL_API_ADDR=:8081`, `POSTGRES_DSN`, `CLICKHOUSE_ENDPOINT`
  (host:9000/9440 native), `CLICKHOUSE_DATABASE` (`telemetry`),
  `CLICKHOUSE_USERNAME`, `CLICKHOUSE_PASSWORD`. Optional: `ALERT_EVAL_INTERVAL`,
  `ALERT_DELIVERY_POLL`. Auth is native by default (no Keycloak required); set
  the OIDC issuer for SSO.
- **cell-ingest**: `CELL_INGEST_ADDR=:4318`, same `CLICKHOUSE_*`, plus the
  Prometheus remote-write target when metric ingest is enabled.

Email alert channels send over SMTP per-channel (configured in the UI), so no
global mail config is needed — just ensure outbound SMTP egress is allowed.

## Deploy

### 1. Build + push images (build context = repo root)
```bash
az acr login -n <acr>
docker build -f services/cell-api/Dockerfile    -t <acr>.azurecr.io/cell-api:<tag>    .
docker build -f services/cell-ingest/Dockerfile -t <acr>.azurecr.io/cell-ingest:<tag> .
docker push <acr>.azurecr.io/cell-api:<tag>
docker push <acr>.azurecr.io/cell-ingest:<tag>
```
(Or `az acr build -r <acr> -f services/cell-api/Dockerfile -t cell-api:<tag> .` to
build server-side.)

### 2. Provision the data stores
- Azure Database for PostgreSQL Flexible Server → build `POSTGRES_DSN`
  (`?sslmode=require`).
- ClickHouse Cloud (or AKS/VM) → `CLICKHOUSE_ENDPOINT` + creds; the schema /
  `telemetry` DB is created by cell-ingest/cell-api on first use.
- (Optional) managed Prometheus for metric alerts.

### 3. Deploy the apps (Bicep)
See [`deploy/azure/main.bicep`](../deploy/azure/main.bicep):
```bash
az deployment group create -g <rg> -f deploy/azure/main.bicep \
  -p acrLoginServer=<acr>.azurecr.io acrName=<acr> imageTag=<tag> \
     postgresDsn='postgres://user:pass@host:5432/sluicio?sslmode=require' \
     clickhouseEndpoint='xxx.azure.clickhouse.cloud:9440' \
     clickhouseUsername='default' clickhousePassword='********'
```
It creates a Log Analytics workspace, the ACA environment, the two apps
(cell-api pinned to 1 replica; cell-ingest 1–5), assigns **AcrPull** to each
app's managed identity, and outputs the public URLs. For production, add VNet
integration to the environment so the apps reach Postgres + ClickHouse over
private endpoints.

### 4. Frontend
Deploy `frontend/` to Azure Static Web Apps; configure its API base / SWA
proxy to forward `/api` to the cell-api URL from the Bicep output. (Or add a
frontend build stage + `go:embed` to the cell-api image to serve the SPA
itself — a small change, not done today.)

### 5. Point telemetry at the cell
Configure your OTel Collectors' OTLP/HTTP exporter endpoint to the cell-ingest
URL, then sign in to the cell-api URL (native admin on first boot).

## Why not just ACA for everything?
ClickHouse and Postgres are stateful and want block storage + careful scaling —
ACA's ephemeral, autoscaling model fights that. If you'd prefer one managed
platform end-to-end, **AKS** is lower-friction: the repo's first-class artifact
is the Helm chart at `deploy/helm/cell`, and ClickHouse can run via the
ClickHouse/Altinity operator in the same cluster.
