<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Sluicio quickstart

The whole stack — Postgres, ClickHouse, and the Sluicio services — from one
compose file, no configuration. You don't even need to clone the repo:

```bash
curl -LO https://raw.githubusercontent.com/SLUICIO/sluicio-app/main/deploy/quickstart/docker-compose.yml
docker compose up -d
```

(From a clone: `docker compose -f deploy/quickstart/docker-compose.yml up -d`.)

Then open **http://localhost:8080** and sign in with the seeded admin account
— `admin@sluicio.local` / `admin` — and change the password from the user
menu. That's it — you're running Sluicio (Community edition).

- **Send it telemetry:** point an OpenTelemetry Collector (or any OTLP/HTTP
  exporter) at **http://localhost:4318**. See
  [`../otel-collector/`](../otel-collector/) for a ready collector config.
- **Stop it:** `docker compose -f deploy/quickstart/docker-compose.yml down`
  (add `-v` to also delete the data).
- **Try Enterprise features:** set `SLUICIO_LICENSE_KEY` in your environment
  before `up` (see [`CONTRIBUTING.md`](../../CONTRIBUTING.md) for a dev key).

> ⚠ **Evaluation / local use only.** This ships weak default passwords and no
> TLS. For a real deployment use [`../server/`](../server/) (single host,
> behind Caddy with real secrets and backups) or the
> [Helm chart](../helm/cell/) (Kubernetes). Both let you bundle the databases
> or bring your own — see their READMEs.
