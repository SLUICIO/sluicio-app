<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->
# Cost model — one Sluicio cell on Azure

> **Disclaimer.** Rough Azure **pay-as-you-go list-price** estimates (US
> regions, late 2025), for order-of-magnitude planning — not a quote. Actual
> cost varies by region and changes over time; **reserved instances / savings
> plans cut node cost ~30–60%**. Verify with the Azure Pricing Calculator.
> Excludes Azure support plans.

## TL;DR
At low telemetry volume, **a cell's cost is a fixed compute floor, not a
function of data.** A cell that ingests 5 GB/month costs about the same as one
ingesting 500 GB/month. Plan per-instance economics around "keeping the
platform running," not GB.

Reference workload: **5 GB telemetry/month, 14-day retention, one cell.**

| Tier | What it is | ~ $/month |
|---|---|---|
| **Budget** | Single VM, docker-compose (no HA) | **$80–140** |
| **AKS (recommended)** | `deploy/helm/cell` + ClickHouse operator | **$150–230** |
| **Managed** | ACA apps + ClickHouse Cloud + Azure PostgreSQL | **$110–280** |

## Why data volume barely matters here
- 5 GB/mo × (14 ÷ 30) ≈ **2.3 GB raw resident** at steady state.
- ClickHouse compresses logs/traces ~5–10× → **~0.2–0.5 GB actually stored.**
- The smallest provisioned Premium SSD (32–64 GiB) is >95% empty. You pay for
  *provisioned* size (~$5/mo), not data.
- Network egress is ~$0 (well under the ~100 GB/mo free allowance).

So storage + egress are a rounding error. The bill is always-on compute:
nodes, managed Postgres, and the load balancer.

## Tier detail

### Budget — single VM (docker-compose), no HA: ~$80–140/mo
| Item | ~$/mo |
|---|---|
| 1× VM (B2ms 2vCPU/8GiB → B4ms 4vCPU/16GiB if ClickHouse needs headroom) | 60–120 |
| Premium SSD 64 GiB + public IP | 14 |
| ACR Basic | 5 |

Postgres + ClickHouse + cell-api + cell-ingest on one box. Cheapest way to run
one small instance; no redundancy, you own backups/patching.

### AKS — minimal (the path in [docs/aks.md](aks.md)): ~$150–230/mo
| Item | ~$/mo |
|---|---|
| AKS control plane (Free tier) | 0 |
| Nodes: 2× B2ms (or 1× D4s_v5) | 120–140 |
| ClickHouse Premium SSD (32–64 GiB) | 5–10 |
| Azure Database for PostgreSQL (Burstable B1ms + 32 GiB) | ~20 |
| Standard Load Balancer + public IP | ~24 |
| ACR Basic | 5 |
| Azure Static Web Apps (frontend, Free tier) | 0 |

Squeezing onto a single small node (no HA) lands ~$110–130. Managed Prometheus
adds a little **only if** you use metric alerts (negligible at this scale).

### Managed — ACA + ClickHouse Cloud + Azure PostgreSQL: ~$110–280/mo
| Item | ~$/mo |
|---|---|
| ACA apps (cell-api warm at 1 replica + cell-ingest) | ~40 |
| Azure Database for PostgreSQL (Burstable) | ~20 |
| **ClickHouse Cloud (smallest production service)** | **50–200** |
| Static Web Apps (Free) | 0 |

Lowest ops, but ClickHouse Cloud's floor dominates and is driven by its minimum
service size, not by 5 GB of data. (See [docs/azure-container-apps.md](azure-container-apps.md)
for why ClickHouse itself shouldn't run *in* Container Apps.)

## The economic consequence: shared vs dedicated cells
Because a dedicated cell costs **~$80–230/month no matter how little data flows
through it**, one-cell-per-small-customer is expensive per tenant — the fixed
floor swamps a 5 GB workload.

- **Shared cell** (many orgs on one cell, isolated by `organization_id`):
  amortizes the ~$150 floor across all tenants. The cost-sane model for small /
  free-tier customers. Marginal cost of an extra 5 GB tenant ≈ a few dollars.
- **Dedicated cell** (one cell per customer): reserve for customers needing
  hard isolation (healthcare/finance) or high volume, where they justify the
  full floor on their own.

This is exactly the hybrid the architecture is built for (see
[docs/decisions.md](decisions.md) D-004): a control plane routing tenants to
shared or dedicated cells.

## Levers if you need it cheaper
- **Reserved instances / savings plans** on the node pool / Postgres: −30–60%.
- **Shared cells** instead of dedicated (above) — the biggest lever per tenant.
- Drop to **single-node** (no HA) for non-critical instances.
- Shorter retention than 14 days shrinks an already-tiny disk — not worth it at
  this volume.
- B-series **burstable** VMs for low, spiky load (already assumed above).
