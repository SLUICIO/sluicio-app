# MCP server

Status: **shipped** — two transports (Streamable HTTP + stdio), protocol
`2025-06-18` (also speaks `2025-03-26` and `2024-11-05`).

Sluicio ships a [Model Context Protocol](https://modelcontextprotocol.io) server
so an AI client (Claude, Cursor, a custom agent platform) can answer questions
about a cell from live data — "which integrations are unhealthy?", "show the
order-bus system's members", "what's spiking in the metrics?" — and, within
limits, suggest a change.

## Design

- **Thin client over `/api/v1`.** Every tool is a call against the cell-api REST
  surface — no new backend logic, no second source of truth.
- **One shared core** (`pkg/mcp`): the tool catalogue + JSON-RPC handling. Both
  transports embed it.
- **Least-privilege auth.** Authenticate with a Sluicio Bearer token; use a
  **scoped viewer service-account token** (Settings → Service accounts) so the
  assistant can observe but never mutate. The token role cap (docs/api.md
  phase C) enforces read-only, and the account's *scope* bounds WHAT it reads: a
  scoped SA sees only the services its group memberships grant — per-signal
  grants included — so an assistant can be handed "team A's logs and metrics"
  and nothing else (docs/service-account-scoping-design.md). MCP inherits all of
  this from REST automatically; there is no MCP-side filtering to configure.
- **Read-only, with one deliberate exception.** An agent may FILE a proposal; it
  can never apply one. See "The one tool that writes" below.
- **Curated catalogue** — a small set keeps the model's tool selection accurate.

## Tools

Start with `sluicio_cell_brief`: one call that answers "what am I looking at,
and is anything wrong?", so an agent orients in a single round trip instead of
five.

| Tool | What it returns |
| --- | --- |
| `sluicio_cell_brief` | **Start here.** Org + environment name, counts, what's firing now (worst first, with runbooks), services no rule watches, pending proposals (arg: `window`) |
| `sluicio_list_integrations` | integrations + rolled-up health |
| `sluicio_list_services` | discovered services + health + service facets |
| `sluicio_list_systems` | systems + rolled-up health |
| `sluicio_get_system` | one system + member services (arg: `id`) |
| `sluicio_system_types` | the system-types catalog, each with its docs.sluicio.com URL |
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
| `sluicio_propose_check_tuning` | **files a proposal** to retune an existing alert rule (args: `rule_id`, `rationale`, + the fields to change) |

Every tool carries **annotations** (`readOnlyHint`, `destructiveHint`,
`idempotentHint`, `openWorldHint`) so a client can present the read surface as
safe and stop asking for confirmation seventeen times — and can tell the one
writer apart from the rest.

Every tool also declares an **output schema** (`pkg/mcp/output.go`), and results
carry `structuredContent` alongside the text block. A client knows the shape
before it calls, so a model doesn't spend a turn discovering that services live
under `services`. The schemas describe what is worth keying off rather than
every field present, and allow extra properties — the API must stay free to grow
without breaking agent calls.

### The one tool that writes

`sluicio_propose_check_tuning` does not change monitoring config. It files a
**proposal**: a stored, reviewable change request with a diff and the agent's
rationale, which a human with edit rights approves or rejects in the Proposals
inbox. Only their approval applies it, through the same code path as an edit
made by hand. The cell snapshots the current values itself rather than trusting
the caller's, so a human's concurrent edit blocks approval instead of being
silently reverted. The inbox lives at Proposals in the nav; the primitive is
described in issue #8 (WS2).

## Transport A — Remote (Streamable HTTP), recommended for deployed cells

Served by **cell-api** at:

```
POST  https://<your-host>/api/v1/mcp
```

It's **mounted on cell-api**, so it ships in **every deployment** — dev
`docker-compose`, the single-server Caddy setup (`/api/*` already proxies to
cell-api), and the Helm chart (same ingress). **No separate service, port, proxy
rule, or TLS cert.** Auth is the normal `Authorization: Bearer <token>`, so it
reuses the existing auth + RBAC + role cap; tool calls are re-dispatched
internally over loopback as the caller's principal.

Connect from a client that supports **remote/custom MCP connectors**: add a
custom connector pointing at `https://<your-host>/api/v1/mcp` with a viewer
service-account token. This is the right transport for clients that run in a
sandbox (e.g. Claude Desktop **Cowork**, which can't reach a host binary or
`localhost`).

**Transport details**, each a spec "MAY" answered deliberately:

- **Stateless.** No session is created and no `Mcp-Session-Id` is issued: every
  request carries its own token and every dispatch is independent. The endpoint
  therefore survives a cell restart mid-conversation. A client that sends a
  session id anyway is not penalised — the header is ignored.
- **`GET` and `DELETE` answer `405`** with a body saying why. There are no
  server-initiated messages to stream: alerts reach agents through webhooks and
  event subscriptions (docs/outbound-events-design.md), which survive a
  disconnect where a held SSE stream would not. There are no sessions to delete.
- **`POST` answers JSON**, or `text/event-stream` for a client that accepts
  nothing else. One request, one response — a stream buys nothing.
- **`MCP-Protocol-Version`** is validated; an unknown revision is a `400` naming
  what the cell speaks. `initialize` agrees with the client's revision when it
  can and answers with its own when it can't, rather than echoing a claim it
  cannot back.
- **Cross-origin calls must carry a Bearer token.** A page open in the user's
  browser can POST here and the browser will attach their session cookie
  unasked; refusing ambient credentials from a foreign origin closes that
  without an allowlist that would lock out legitimate browser-based clients.

**OAuth** is available for connectors that require it rather than a static
token: discovery metadata (`/.well-known/oauth-protected-resource`,
`/.well-known/oauth-authorization-server`), dynamic client registration
(`POST /api/v1/oauth/register`), an authorize/consent screen, and a token
endpoint all ship with cell-api.

**Rate limits.** Token-authenticated callers get a generous per-caller ceiling
(600/min, burst 120) keyed on the service account, not the individual token.
Browser sessions are never limited. A blocked call returns `429` with
`Retry-After`. See `services/cell-api/internal/api/ratelimit.go`.

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

## Audit

Writes an agent performs through MCP are recorded with `via: mcp`, so an admin
can filter the audit log by channel and answer "what did the agents do?". The
marker is a per-process secret rather than a header a client could set, so a
caller can neither disguise its own writes as an agent's nor the reverse.

## Future

- **More proposal kinds** — maintenance windows, applying a monitoring template.
- **Direct-execute for genuinely low-risk verbs** — acknowledge an alert,
  annotate an instance.
