<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Developer surface — API, MCP & event subscriptions

| Field | Value |
|-------|-------|
| **Area** | The API & MCP page: tokens, REST/OpenAPI, the MCP server, outbound event subscriptions |
| **Automation status** | Partial (event subscriptions + MCP RBAC automated; assistant clients manual) |
| **Automated by** | [event-subscriptions.spec.ts](../../../e2e/tests/event-subscriptions.spec.ts), [rbac.spec.ts](../../../e2e/tests/rbac.spec.ts) (MCP surface) |
| **Last reviewed** | 2026-07-25 |

## Preconditions
- Stack up, admin session. For events: at least one **webhook** notification channel pointing somewhere you can observe (a request-bin, n8n, or a local sink). For MCP: a service-account token.

## API & MCP

### Case 1 — Tokens and the REST surface
- **Surface:** API & MCP (route `/developers`).
- **Steps:** Mint a personal access token (Account → Tokens) and a service-account token (Settings → Service accounts) → call `GET /api/v1/integrations` with each → open the API reference and `llms.txt` links.
- **Expected:** Both tokens authenticate; the SA token's role and group scope bound what it returns (a group-less scoped SA sees nothing). The reference and OpenAPI/llms.txt links load.
- **Automation:** Partial.

### Case 2 — MCP server from a real client
- **Steps:** Add the connector URL (`<cell>/api/v1/mcp`) in Claude Desktop/Code or Cursor with a **viewer** service-account token → ask "which integrations are unhealthy?" and "what are we storing that nobody alerts on?"
- **Expected:** Tools resolve read-only data scoped to the token; the usage-report tool (`sluicio_usage_report`) answers the second question and is **refused for non-admin tokens**. No tool can mutate anything.
- **Automation:** Partial — RBAC parity + the usage-report gate are automated (`rbac.spec.ts`); a real client session is manual.

## Event subscriptions (v0.11.40+)

### Case 3 — Create a subscription and receive an event
- **Surface:** API & MCP → Event subscriptions. **Endpoints:** `GET/POST /api/v1/event-subscriptions`, `GET /api/v1/event-types`
- **Steps:** "+ New subscription" → scope (org-wide or a team) → pick a webhook channel (the hint links to channel management) → tick filters from the catalog and/or add a custom glob → save. Then create or rename an integration.
- **Expected:** Within ~5s the destination receives `com.sluicio.integration.created` (or `.updated`). Default payload is the canonical flat JSON `{event, id, time, subject, source, data}`; a channel whose payload format is CloudEvents receives a CE 1.0 envelope (`application/cloudevents+json`) instead. HMAC headers appear when the channel has a secret. At least one filter is required at create (`*` allowed but must be chosen).
- **Automation:** yes.

### Case 4 — Filters actually filter
- **Steps:** With a subscription filtered to `com.sluicio.integration.*`, perform an unrelated mutation (create a group, edit retention).
- **Expected:** Nothing is delivered to that subscription; a second subscription filtered `*` receives it. Note that subscription CRUD is itself audited, so a `*` subscription sees its own creation event — that's correct, not a bug.
- **Automation:** yes.

### Case 5 — The delivery ledger diagnoses a broken endpoint
- **Steps:** Point a subscription at a URL that refuses connections → trigger a matching event → expand **"Deliveries ▸"** on the row.
- **Expected:** The row shows the event type, when, state (pending → failed after 5 attempts), the attempt count and the last error text. A working endpoint shows "delivered". The header states the semantics: newest 50, finished deliveries kept 3 days.
- **Automation:** Partial — the delivered path automated; the failure path manual.

### Case 6 — Team scoping and permissions
- **Steps:** As an org admin, create a TEAM-scoped subscription for a team with limited visibility → trigger events on an entity that team can see and on one it cannot → also try creating an org-wide subscription as a viewer.
- **Expected:** The team subscription receives only events on entities within its visibility; org-administration events (members, groups, settings) never reach a team subscription. A viewer gets **403** creating org-wide; team subscriptions are manageable by org editors, or by that team's editors with the EE `rbac_advanced` entitlement.
- **Automation:** Partial — the viewer 403 automated; the visibility split manual (needs a scoped team).

### Case 7 — Operational events
- **Steps:** Subscribe to `com.sluicio.alert.fired`, `com.sluicio.alert.resolved`, `com.sluicio.errors.opened`, `com.sluicio.service.discovered` → fire an alert, let it resolve, ingest errors on a fresh service.
- **Expected:** `alert.fired` arrives when a real notification goes out (a maintenance-suppressed firing emits nothing — verify by firing inside a window); `service.discovered` arrives once, the first time the service enters the catalog.
- **Automation:** manual (needs live telemetry + timing).

## Notes
- Events are **best-effort notifications**; the audit log remains the tamper-evident record. Never present a subscription as a compliance control.
- Webhook payloads and PagerDuty events are deliberately not templatable — consumers parse them (see [alerts-notifications.md](alerts-notifications.md) for what IS templatable).
