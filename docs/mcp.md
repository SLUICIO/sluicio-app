# MCP server

Status: **v1 shipped** (2026-06-25) — two transports (HTTP + stdio).

Sluicio ships a [Model Context Protocol](https://modelcontextprotocol.io) server
so an AI client (Claude Desktop, Cursor, …) can answer questions about a cell
from live data — "which integrations are unhealthy?", "show the order-bus
system's members", "what's spiking in the metrics?".

## Design

- **Thin client over `/api/v1`.** Every tool is a GET against the cell-api REST
  surface — no new backend logic.
- **One shared core** (`pkg/mcp`): the tool catalogue + JSON-RPC handling. Two
  transports embed it (below).
- **Least-privilege auth.** Authenticate with a Sluicio Bearer token; use a
  **scoped viewer service-account token** (Settings → Service accounts) so the
  assistant can observe but never mutate. The token role cap (docs/api.md
  phase C) enforces read-only even though every tool is already read-only,
  and the account's *scope* bounds WHAT it reads: a scoped SA sees only the
  services its group memberships grant — per-signal grants included — so an
  assistant can be handed "team A's logs and metrics" and nothing else
  (docs/service-account-scoping-design.md). MCP inherits all of this from
  REST automatically; there is no MCP-side filtering to configure.
- **Curated, read-only tools** — a small set keeps the model's tool selection
  accurate.

## Tools

| Tool | What it returns |
| --- | --- |
| `sluicio_list_integrations` | integrations + rolled-up health |
| `sluicio_list_services` | discovered services + health |
| `sluicio_list_systems` | systems + rolled-up health |
| `sluicio_get_system` | one system + member services (arg: `id`) |
| `sluicio_system_types` | the system-types catalog |
| `sluicio_errors` | the "in trouble" feed (arg: `window`) |
| `sluicio_health` | what's unhealthy and WHY — entities grouped with their failing checks (arg: `window`) |
| `sluicio_error_report` | errors-since-a-time triage, grouped with the causing checks (arg: `since`) |
| `sluicio_alert_instances` | recent alert-rule firings with state + severity (arg: `limit`) |
| `sluicio_digest` | since-last-visit digest |
| `sluicio_get_integration` | one integration + per-service health (args: `id`, `window`) |
| `sluicio_metric_catalog` | metric catalog search (args: `window`, `query`, `service`) |
| `sluicio_metric_series` | one metric's time series per service (args: `metric`, `service`, `window`) |
| `sluicio_search_traces` | search traces by `service` / `errors_only` / `query` / `window` (up to `limit`; `next_cursor` ⇒ more) |
| `sluicio_get_trace` | one trace by id — all its spans (arg: `trace_id`) |
| `sluicio_search_logs` | search logs by `query` / `min_severity` / `service` / `integration` / `attrs` / `window` |
| `sluicio_usage_report` | the admin usage report: per-signal unused-by-alerts share, storage estimates, per-service coverage (arg: `window`; needs an admin token) |

## Transport A — Remote (HTTP), recommended for deployed cells

Served by **cell-api** at:

```
POST  https://<your-host>/api/v1/mcp
```

It's **mounted on cell-api**, so it ships with cell-api in **every deployment**
— dev `docker-compose`, the single-server Caddy setup (`/api/*` already proxies
to cell-api), and the Helm chart (same ingress). **No separate service, port,
proxy rule, or TLS cert.** Auth is the normal `Authorization: Bearer <token>`,
so it reuses the existing auth + RBAC + role cap; tool calls are re-dispatched
internally over loopback as the caller's principal.

Connect from a client that supports **remote/custom MCP connectors**: add a
custom connector pointing at `https://<your-host>/api/v1/mcp` with a viewer
service-account token. This is the right transport for clients that run in a
sandbox (e.g. Claude Desktop **Cowork**, which can't reach a host binary or
`localhost`).

*Auth note:* if a client's "custom connector" flow requires **OAuth** rather
than a static Bearer token, an OAuth profile in front of `/api/v1/mcp` is a
follow-up (the MCP spec defines one). Plain Bearer works for clients that accept
a URL + token/header.

## Transport B — Local (stdio), for host-run clients

The `services/cell-mcp` binary speaks stdio (newline-delimited JSON-RPC) for
clients that spawn a local process (classic Claude Desktop chat, Cursor):

```bash
make mcp        # builds bin/cell-mcp
```

`mcpServers` config (env: `SLUICIO_BASE_URL`, `SLUICIO_TOKEN`):

```json
{
  "mcpServers": {
    "sluicio": {
      "command": "/path/to/bin/cell-mcp",
      "env": {
        "SLUICIO_BASE_URL": "https://sluicio.example.com",
        "SLUICIO_TOKEN": "con_sa_…"
      }
    }
  }
}
```

Note: stdio runs on the host, so it can't be used by sandboxed clients (Cowork)
— use Transport A there.

## Future

- **OAuth profile** for `/api/v1/mcp` if a target client requires it.
- **More read tools** (logs/traces search, a service's health checks).
- **Safe writes** behind a flag (acknowledge an error, annotate) — needs a
  non-viewer token + explicit guardrails.
