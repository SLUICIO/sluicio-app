# Modeling integrations in OpenTelemetry: an attribute convention for message flows

**Pillar:** Category & Concepts (technical) · **Channel:** Blog (+ optional LinkedIn teaser)
**Angle:** Vendor-neutral OTel guidance. How to instrument *integrations* — not just services — so a trace tells the whole story of a message. Reads as an OpenTelemetry article; Sluicio is mentioned once, at the end.

---

**Title:** Modeling Integrations in OpenTelemetry: An Attribute Convention for Message Flows
**Slug:** otel-attributes-for-integration-flows
**Meta description:** OpenTelemetry's semantic conventions describe services and messaging well — but not the *integration* as a first-class thing. Here's a practical attribute convention for making a trace tell the full story of a message as it crosses systems.

---

OpenTelemetry gives us excellent building blocks: spans, trace context, and a growing set of semantic conventions for HTTP, messaging, databases, and more. What it doesn't give us, out of the box, is a first-class notion of an **integration** — the end-to-end journey a message takes as it crosses systems, queues, and organizations.

If you run an integration estate, that journey *is* the thing you care about. A trace shouldn't just tell you "service X called service Y." It should tell you: *this order, for this partner, on this flow, entered here, took these hops, and stalled there.* Getting that out of OTel is mostly a matter of being disciplined and consistent about **attributes**.

This is the convention we'd argue for. It builds on existing OTel semantic conventions wherever they exist and only adds a small, stable namespace where they don't.

## Principle 1: the integration is a resource-level identity, not a guess

The single most useful thing you can do is stop inferring "which integration is this?" from span names and start declaring it explicitly. Put a stable identity on every span that participates in a flow:

| Attribute | Example | Cardinality | Why |
|---|---|---|---|
| `integration.id` | `vaccindirekt-002` | low | Stable slug for the flow. The join key across every hop. |
| `integration.name` | `Export from data layer to Voyado` | low | Human-readable; for dashboards, not joins. |
| `integration.direction` | `inbound` \| `outbound` \| `internal` | low | Lets you separate "we received" from "we sent." |
| `integration.region` | `SE` | low | Data residency, routing, on-call ownership. |
| `integration.priority` | `p1` | low | Drives alerting severity and SLOs. |

Where these belong: the stable ones (`integration.id`, `region`, `priority`) are a great fit for **resource attributes** on the emitting service, so every span inherits them for free. The per-message ones (below) go on the spans.

The payoff: every query, dashboard, and alert keys off `integration.id` instead of brittle string-matching on span names. When a flow misbehaves, you filter to one value and see only its spans.

## Principle 2: reuse the messaging conventions — don't reinvent them

Most integration hops are messaging hops, and OTel already has solid conventions for them. Use them as-is before adding anything custom:

| Attribute | Example | Notes |
|---|---|---|
| `messaging.system` | `activemq`, `kafka`, `servicebus` | The broker, named by OTel's registry. |
| `messaging.operation` | `publish`, `receive`, `process` | Lets you tell "queued" from "consumed." |
| `messaging.destination.name` | `orders.partner.inbound` | The queue/topic. |
| `messaging.message.id` | `b3f1…` | Broker message id (high cardinality — search, don't group). |
| `server.address` / `server.port` | `mq.internal:61616` | The endpoint, from the general conventions. |
| `error.type` | `timeout`, `ConnectionRefused` | Low-cardinality error class for grouping. |

The rule of thumb: if OTel has a convention, the custom `integration.*` namespace should never duplicate it. Custom attributes exist only for concepts the standard doesn't cover — and "an integration as a unit of work" is the main one.

## Principle 3: carry the business identifier, carefully

The question every integration owner eventually asks an incident is *"where is order 10042?"* A trace can answer that instantly — but only if a business correlation key is on the spans, and only if you're disciplined about cardinality and PII.

| Attribute | Example | Guidance |
|---|---|---|
| `integration.message.type` | `order`, `invoice`, `consent` | Low cardinality. Great for grouping. |
| `integration.correlation.id` | `ORD-10042` | High cardinality. Index for **search**, never for metric labels. |
| `integration.counterparty` | `voyado` | The partner/system on the other side. Low-ish cardinality. |
| `integration.batch.size` | `500` | For batch/file flows; explains duration spikes. |

Two hard rules. First, **never put a high-cardinality value (an order id, an email) into a metric dimension** — that's how you blow up storage and cost. Keep it on the span for search; derive metrics from the low-cardinality attributes. Second, **don't put PII in attributes.** Use a non-identifying business key (`ORD-10042`), or hash it. A trace is telemetry, not a data store.

## Principle 4: model the seams — links, retries, and dead-letters

The hardest part of integration tracing is the seams: a message lands on a queue at 02:00 and is consumed at 02:14 by a different process. If you can propagate W3C trace context through the broker, do — that keeps it one trace. When you can't (many brokers, batch jobs, file drops), don't fake a parent/child relationship. Use **span links** to connect producer and consumer, and lean on `integration.correlation.id` to stitch the story together at query time.

Represent the things that go wrong as **span events**, not as lost information:

- a retry → an event `retry` with attribute `retry.count`
- a dead-letter → an event `dead_letter` with the reason
- a delay/SLA breach → set span status `Error` with a clear `error.type`, so it surfaces without reading the message body

The point is that "the message stalled and was dead-lettered at 02:14" should be a *queryable fact in the trace*, not something you reconstruct from logs across three systems.

## Principle 5: span status and naming should be boring and consistent

Two small disciplines pay off enormously:

**Span status.** A span is `Error` only when the *flow* failed — a delivery that didn't happen, an SLA that was missed, a message that dead-lettered. A 404 that's an expected "not found" in a lookup is not a flow error. Consistent status is what makes "show me failed flows" trustworthy.

**Span names — low cardinality, always.** Name the operation, not the instance: `process orders.partner.inbound`, never `process order 10042`. The instance goes in attributes. This is straight from OTel's guidance, and it's the difference between a clean operation list and an unusable one.

## A worked example

A single outbound flow, one message, two hops, expressed as attributes:

```
Resource (on the emitting service):
  service.name             = vaccindirekt-002-worker
  deployment.environment   = production
  integration.id           = vaccindirekt-002
  integration.region       = SE
  integration.priority     = p1

Span 1  "process datalayer.export"
  integration.direction        = outbound
  integration.message.type     = customer
  integration.correlation.id   = CUST-88231     (indexed for search)
  integration.counterparty     = voyado

Span 2  "publish voyado.api.outbound"   (child / linked)
  messaging.system             = http
  server.address               = api.voyado.com
  http.response.status_code     = 200
  span.status                  = Ok
```

Filtering to `integration.id = vaccindirekt-002` gives every span for that flow. Adding `integration.correlation.id = CUST-88231` finds one customer's journey end to end. Grouping by `error.type` over `integration.priority = p1` gives you an SLO view — all without parsing a single payload.

## Why this matters

None of this requires anything outside OpenTelemetry. It's the standard, applied with a consistent attribute vocabulary and a bit of cardinality hygiene. The result is traces that describe *integrations* as first-class things: searchable by business key, groupable by flow, and honest about where messages actually fail.

That's the convention we've built [Sluicio](https://sluicio.com) around — OTel-native, so if you instrument your flows this way, they light up without any proprietary agent. But the convention stands on its own: adopt it with any OpenTelemetry backend and your integration traces get dramatically more useful.

---

### Notes
- Reads as vendor-neutral OTel guidance; single Sluicio mention at the end (swap the URL for your real domain).
- Attribute names follow OTel style (lowercase, dotted namespaces). `integration.*` is the proposed custom namespace; everything else maps to existing OTel conventions.
- Examples are grounded in your real integration model (e.g., `vaccindirekt-002`, region `SE`, priority `p1`, counterparty Voyado). Confirm the exact span attributes your collector emits and I'll align names 1:1.
- Optional LinkedIn teaser: 4–5 lines — "OTel models services well. It doesn't model the *integration*. Here's the attribute convention we use to fix that 👇 [link]".
