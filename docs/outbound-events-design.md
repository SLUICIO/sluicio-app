<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Outbound events (design)

Status: **part 1 shipped 2026-07-15; part 2 draft for review.**
Companion issue: #4.

Two related pieces:

1. **CloudEvents as an opt-in payload format for alert webhooks** —
   shipped, documented here because it establishes the event vocabulary
   part 2 extends.
2. **Event subscriptions** — "tell my platform when things happen in
   Sluicio" (`integration.created`, `service.discovered`, …) — the
   roadmap feature this doc exists to review.

## Why CloudEvents

CloudEvents 1.0 is the CNCF-graduated envelope for exactly this: a tiny
set of required attributes (`id`, `source`, `type`, `time`) around an
arbitrary `data` payload. Receivers that speak it — Azure Event Grid
(native), AWS EventBridge, Google Eventarc, Knative, Dapr, n8n,
TriggerMesh — can route, filter, and dedupe Sluicio events without
reading our docs. `id` gives consumers idempotency (our deliveries are
at-least-once with retries), `type` gives them routing, `source` tells
them which cell. For an OTel-native product, emitting CNCF-standard
events is the obvious posture.

## Part 1 — shipped: CE format on alert webhooks

Webhook channels accept `config.format = "cloudevents"` (UI: "Payload
format"). Default (empty) stays the bare canonical JSON — the existing
receiver contract is pinned by tests and untouched. When enabled,
deliveries are CloudEvents 1.0 **structured mode**
(`Content-Type: application/cloudevents+json`):

```json
{
  "specversion": "1.0",
  "id": "<alert-instance-id>.firing",
  "source": "<cell public URL, or urn:sluicio:cell>",
  "type": "com.sluicio.alert.fired",
  "time": "2026-07-15T12:00:00Z",
  "subject": "<rule id>",
  "datacontenttype": "application/json",
  "data": { …the canonical webhook payload, unchanged… }
}
```

- Types are reverse-DNS: `com.sluicio.alert.fired` /
  `com.sluicio.alert.resolved`. This is the vocabulary part 2 extends.
- `id` is stable per (alert instance, state): retries and duplicate
  routes dedupe; firing and resolved stay distinct events.
- HMAC signing (docs/webhook-signing.md) composes unchanged — it signs
  the raw body regardless of format.
- Slack/PagerDuty/email are not affected; the format knob is
  webhook-only (validated).
- The error notifier's "Errors detected" sends through a CE channel are
  wrapped too (random id — they have no instance).

## Part 2 — proposed: event subscriptions

**The insight: the audit recorder's action vocabulary already IS the
domain-event taxonomy.** Every config mutation flows through
`recordAudit(action, entity, id, metadata)` — `integration.created`,
`group_member.added`, `service_account.updated`, `system_settings.update`,
…. Event subscriptions tap the same recording points (not the audit
chain itself) and fan out to channels.

- **Entity**: `event_subscriptions` — name, event-type filters (globs:
  `com.sluicio.integration.*`), destination notification channel
  (reusing channels + the delivery ledger + retries + HMAC signing),
  `group_id` team scoping exactly like dashboards/alert rules, enabled
  flag. Admin/editor managed, audited.
- **Event types**: `com.sluicio.<entity>.<verb>` derived mechanically
  from audit actions (`integration.created` →
  `com.sluicio.integration.created`), plus the non-audit families:
  `com.sluicio.alert.fired/.resolved` (part 1),
  `com.sluicio.errors.opened` (the error notifier),
  `com.sluicio.service.discovered` (catalog reconciler),
  `com.sluicio.maintenance.started/.ended`.
- **Payload**: CE-only from day one — no legacy shape to preserve.
  `data` carries the audited metadata (already scrubbed of secrets:
  channel configs, token plaintexts never enter audit metadata) plus
  entity natural keys.
- **Boundary with the audit log**: audit stays the tamper-evident,
  hash-chained *record* (EE, compliance); events are best-effort
  *notifications* (at-least-once, prunable, CE). An event subscription
  is never a compliance substitute — the docs must say so.
- **Volume control**: type filters are mandatory at create (`*` allowed
  but explicit); per-subscription delivery uses the existing queue with
  its backoff/attempt caps so a dead receiver can't back-pressure the
  API path. Recording is fire-and-forget from the handler's
  perspective (enqueue only).
- **RBAC**: a team-scoped subscription only receives events whose
  subject entity the team can see (the same visibility resolution as
  everything else); org-wide subscriptions are admin-only.

### Explicitly out of scope (v1 of part 2)

- Inbound events / event-driven automation inside Sluicio.
- Guaranteed ordering (consumers get `time` + `id`; strict ordering
  needs an outbox we don't want yet).
- A public "event catalog" API (ship the list in docs first).

## Open questions

1. Are config-mutation events (the audit-derived family) wanted in
   part 2 v1, or start with the operational families only (alerts,
   errors, service discovery, maintenance) and add config events later?
2. Should subscriptions be CE-only (proposed) or offer the canonical
   JSON shape too?
3. Team-scoped subscriptions: v1 or admin-only org-wide first?
4. Where in the UI: Alerts → Notification channels area (proposed — it
   reuses channels) or Developers?
