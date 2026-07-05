# Why OpenTelemetry matters for system integration monitoring

**Week 25 · Pillar: Category & Concepts · Channel: LinkedIn (personal) + Sluicio blog**

> Two versions below. The **LinkedIn** one is short and personal — paste it as-is.
> The **Blog** one is longer with headers and a meta description for SEO. Publish both;
> on LinkedIn, link to the blog post in the first comment (not the body) so the post
> isn't down-ranked for an outbound link.

---

## LinkedIn version (personal profile)

Every service shows green. The integration is on fire.

If you run BizTalk, Logic Apps, a queue and some microservices, an order crosses all of them — but no single tool is watching the flow. So the partner feed can silently drop messages for hours while every dashboard says "healthy."

The usual fix is monitoring-tool-specific solutions — each one watching its own corner in its own format. Separate silos, no shared model, no way to follow one transaction across them.

Sluicio takes the other path: a standard. We build on OpenTelemetry and push for OpenTelemetry protocol (OTLP) enablement across the estate — one vendor-neutral format, so the integration becomes something you can actually see, and your telemetry stays yours.

We're in private beta now — the deeper write-up and an intro call are at sluicio.com.

#OpenTelemetry #Observability

---

## Blog version (SEO)

**Title:** Why OpenTelemetry Matters for System Integration Monitoring
**Slug:** /blog/why-opentelemetry-matters-for-integration-monitoring
**Meta description:** Application monitoring watches services in isolation. Integration estates fail in the gaps between them. Here's why OpenTelemetry is the right foundation for monitoring integrations — not just apps.
**Target keywords:** system integration monitoring, OpenTelemetry integration monitoring, monitoring BizTalk / Logic Apps / message queues, vendor-neutral observability

### The problem isn't your services. It's the space between them.

Most observability tools were built to answer one question: *is this service healthy?* They're good at it. CPU, latency, error rate, per service, on a dashboard.

But if you run a real integration estate — BizTalk Server, Azure Functions, Azure Logic Apps, an ActiveMQ Artemis broker, plus your own microservices — that question quietly misses the point. A business transaction (an order, a claim, a payment, a partner EDI exchange) doesn't live inside any one of those systems. It *crosses* them.

So the failure mode that hurts most is the one where every individual service reports green, and the integration between them is on fire. The order-api is fine. The queue is fine. The downstream worker is fine. Meanwhile messages have been stuck or silently dropped for hours, because nothing was watching the flow as a single thing.

That's the gap we mean by **system integration monitoring**: monitoring the integrations a set of services collectively make up, not just the services in isolation.

### Why the old answer — a proprietary agent per platform — makes it worse

The traditional approach is one vendor agent per technology. A BizTalk-specific monitor. An APM agent for the .NET and Go services. Broker metrics scraped separately. Each one is reasonable on its own.

Stacked together, they create three problems:

1. **Three data models.** Each agent describes the world in its own schema and vocabulary. There's no shared notion of "this is the same transaction" across them.
2. **Manual correlation.** When something breaks, an engineer becomes the integration layer — eyeballing timestamps across three tools to reconstruct one flow.
3. **Lock-in.** Your telemetry lands in each vendor's backend in each vendor's format. Leaving, or even just keeping your own history, is expensive.

You end up data-rich and answer-poor. The information exists; it's just scattered across tools that were never designed to describe a cross-system flow.

### What OpenTelemetry actually changes

[OpenTelemetry](https://opentelemetry.io) (OTel) is a vendor-neutral standard for traces, metrics, and logs. Three things about it matter specifically for integration monitoring:

**One wire format across heterogeneous systems.** Whether work happens in a Logic App, a Go microservice, or a message broker, OTel describes it with the same trace-and-span model. A trace can follow a transaction across process and technology boundaries, so "the integration" becomes a first-class object you can actually see — instead of an inference you make by hand.

**Context that travels with the work.** OTel propagates context across service and queue boundaries, so a payment that starts in one service and finishes in another stays a single, connected story rather than two disconnected fragments.

**Your data stays yours.** Because OTLP (the OpenTelemetry protocol) is an open standard, the telemetry isn't tied to one analysis backend. You can route it, retain it, and — importantly for regulated and on-prem teams — keep it inside your own infrastructure.

That last point is easy to undersell. A standard format is also an exit. When the protocol is open, switching tools doesn't mean re-instrumenting everything, and keeping long history doesn't mean paying a vendor's retention premium forever.

### Where Sluicio fits

We're building [Sluicio](#) on exactly this foundation. It ingests telemetry over OTLP, stores it in ClickHouse and Prometheus, and gives you one place to model, visualize, and alert on the *integrations* across a heterogeneous estate — not just the services inside it.

Two deliberate choices follow from the "your data stays yours" principle:

- **Self-hostable.** The same deployment that backs a managed tenant is what you can run in your own Kubernetes cluster, so your telemetry never has to leave your environment.
- **Auditable, no hostage-taking.** The core is source-available under FSL-1.1 — you can read it, modify it, and self-host it; only competing-as-a-service is restricted, and each release becomes Apache 2.0 over time.

### The takeaway

If your outages keep happening in the gaps between green dashboards, the fix isn't another per-platform agent. It's a shared, vendor-neutral way to see the whole flow — which is exactly what OpenTelemetry gives you, and exactly what integration monitoring should be built on.

Sluicio is in **private beta**. If you run a mixed integration estate and this sounds familiar, [join the waitlist](#) or [book an intro call](#) — we're talking to early teams now.

---

*Internal notes for Robert (delete before publishing):*
- *Replace `(#)` links in the blog version: Sluicio homepage, waitlist URL, intro-call/Calendly URL. Product name "Sluicio" used throughout (repo working name is still "Integration Monitor").*
- *LinkedIn: link sluicio.com in the post body (B2B — optimize for qualified clicks, not reach). Do NOT bury it in the comments.*
- *Optional image: a 4-up of green service dashboards with one red "integration" overlay — strong scroll-stopper for the LinkedIn hook.*
- *After publishing, log W25 numbers in the Weekly Metrics sheet on the next run.*
