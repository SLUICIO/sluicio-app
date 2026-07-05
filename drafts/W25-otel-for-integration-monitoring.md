# W25 — Why OpenTelemetry matters for *integration* monitoring (not just app monitoring)

**Pillar:** Category & Concepts · **Format:** How-to / explainer · **Channel:** LinkedIn (personal) + Blog
**Angle:** Problem-first. Sharpen to integration estates — Azure Functions, an iPaaS, Apache Camel, native .NET services, ActiveMQ. (Avoid BizTalk — it can't emit OTel.)

---

## LinkedIn version (personal profile)

Your APM dashboard is green. Every service is healthy. And the partner order that was supposed to hit the ERP three hours ago is just… gone.

If you run integrations, you know this feeling. App monitoring watches *services*. It doesn't watch the *flow between them* — the queue, the mapper, the retry, the dead-letter, the third party that quietly started returning 200s with empty bodies.

That gap is where integration outages live. An Azure Function, an Apache Camel route, a native .NET worker service, an ActiveMQ consumer — each one reports "fine" in isolation while the business process they form together is broken.

OpenTelemetry changes the unit of observation. Instead of "is this service up?", you trace the *message*: where it entered, every hop it took, where it stalled, how long each leg actually took. A span per hop. A trace per flow. Suddenly the dead-letter at 02:14 and the timeout three steps upstream are the same story, not two unrelated alerts.

Three reasons it fits integration estates specifically:

→ **It's vendor-neutral.** One instrumentation standard across Azure Functions, an iPaaS, Apache Camel, native .NET services, and your queues. No per-tech agent zoo.

→ **It follows the message, not the host.** Trace context propagates across process and protocol boundaries — exactly where integration breaks.

→ **You own the data.** OTLP out means the telemetry is yours to keep, query, and retain as long as the audit demands — not metered away.

The shift is small to say and big to feel: stop monitoring services in isolation, start monitoring the flow.

That's the whole reason we're building Sluicio — OTel-native, self-hosted, integration-first. Quietly in private beta now.

If you've ever been paged for a "healthy" system that was very much not delivering, I'd genuinely like to hear how you caught it. 👇

---

## Blog version (Sluicio blog — SEO)

**Title:** Why OpenTelemetry Matters for System Integration Monitoring (Not Just App Monitoring)
**Slug:** otel-for-integration-monitoring
**Meta description:** App monitoring tells you a service is up. It won't tell you a partner order died between the queue and the ERP. Here's why OpenTelemetry is the right foundation for monitoring integrations — Azure Functions, an iPaaS, Apache Camel, native .NET services, and ActiveMQ alike.

### The green-dashboard outage

Picture the on-call story every integration team knows. Every service reports healthy. CPU is fine, memory is fine, the HTTP endpoints all return 200. And yet a partner's purchase order that entered the pipeline at 02:00 never reached the ERP. No alert fired, because nothing that the monitoring *watched* actually failed.

This is the blind spot of classic application performance monitoring (APM): it is built to answer "is this service healthy?" Integration failures rarely look like an unhealthy service. They look like a message that stalled in a queue, a mapping that silently dropped a field, a retry that exhausted, a third party that started returning empty 200s, a dead-letter that nobody is watching.

### Services vs. integrations

App monitoring treats each component as the unit of observation. That works when your system *is* the component. It breaks down when the thing you care about — a business process — is spread across many components from different vendors:

- An **Azure Function** (or an iPaaS flow) hands off to
- an **Apache Camel** route, which drops a message onto
- an **ActiveMQ** queue, consumed by
- a couple of **native .NET** worker services, which finally call a partner API.

Monitor each of those in isolation and every one can be "up" while the flow between them is dead. The outage lives in the *seams* — and the seams are exactly what per-service health checks don't cover.

![Same system shown two ways: the top view shows four green, independently-monitored services all reporting healthy; the bottom view shows the same four components as a single message flow, with the ActiveMQ-to-.NET hop broken and a silent dead-letter at 02:14 that fired no alert.](W25-diagram.svg)
*Same system. One view sees healthy services. The other sees the outage.*

### What OpenTelemetry changes

OpenTelemetry (OTel) is an open, vendor-neutral standard for traces, metrics, and logs. The important shift for integration teams is conceptual, not just technical: OTel lets you make the **message** the unit of observation instead of the host.

With distributed tracing, every hop a message takes becomes a **span**, and the whole journey becomes a **trace**. Trace context propagates across process, protocol, and vendor boundaries — so the dead-letter at 02:14 and the timeout three steps upstream show up as one connected story rather than two unrelated alerts. You can finally ask the questions that matter: *Where did this order stop? Which leg was slow? Is this partner degrading, or are we?*

### Why OTel fits integration estates specifically

**1. Vendor-neutral by design.** Integration estates are heterogeneous on purpose — that's the job. One instrumentation standard spanning Azure Functions, an iPaaS, Apache Camel, native .NET services, and your queues beats maintaining a different proprietary agent for each technology.

**2. It follows the message, not the machine.** Context propagation is built to cross the exact boundaries where integrations fail: between processes, between protocols, between organizations.

**3. You keep your telemetry.** OTel exports over OTLP, so the data is yours. You decide where it lives and how long you retain it — which matters when an audit asks you to reconstruct a transaction from six months ago, and matters again when a metered-retention SaaS bill makes "keep everything" unaffordable.

### The takeaway

Monitoring each service in isolation will keep missing the outages that matter most to an integration team, because those outages happen in the flow between services. OpenTelemetry gives you a vendor-neutral way to trace that flow end to end — which is why we chose it as the foundation for Sluicio, an OTel-native, self-hosted monitoring tool built specifically for integration estates.

Sluicio is in private beta. If you run integrations and want to compare notes — or get on the waitlist — get in touch.

---

### Visual
Embedded above (`W25-diagram.svg`). For LinkedIn, use the raster export `W25-diagram.png` (2400×1800). Both live in this `drafts/` folder.

### Posting checklist
- [ ] LinkedIn from personal profile
- [ ] Blog post published (slug above) for SEO
- [ ] Diagram attached/embedded
- [ ] Reply to every comment; comment on 3–5 OTel/SRE/integration posts
- [ ] Log last week's numbers in Weekly Metrics (intro calls booked → waitlist signups)
