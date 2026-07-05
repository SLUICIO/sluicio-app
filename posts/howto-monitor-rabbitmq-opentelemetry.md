# How to monitor RabbitMQ metrics using OpenTelemetry

**Pillar:** How-To & Use-Cases · **Channel:** Sluicio blog (SEO) + optional LinkedIn teaser
**Angle:** standards-first — why you do this with OpenTelemetry rather than a proprietary agent

**Slug:** /blog/monitor-rabbitmq-metrics-opentelemetry
**Meta description:** RabbitMQ sits in the middle of most integrations — when it backs up, everything downstream stalls. Here's how to monitor RabbitMQ with OpenTelemetry: one vendor-neutral Collector config, your data in any backend, no proprietary agent.
**Target keywords:** monitor RabbitMQ OpenTelemetry, RabbitMQ metrics OTel Collector, rabbitmqreceiver, RabbitMQ queue monitoring

---

## Why monitor RabbitMQ at all

RabbitMQ is rarely the thing you set out to monitor — but it's almost always in the path. A queue sits between the service that publishes work and the service that consumes it. When the queue backs up, when consumers drop to zero, or when the node hits a memory or disk alarm, every integration downstream stalls quietly. The publishing service still returns 200s. The consuming service looks idle. And messages pile up in between.

That's exactly the kind of failure that hides from service-by-service monitoring, which is why the broker deserves first-class telemetry.

## Why OpenTelemetry — not a proprietary agent

The traditional way to monitor RabbitMQ is to install a vendor's agent that knows how to talk to RabbitMQ and ships those metrics into that vendor's cloud, in that vendor's format. It works. But it comes with strings: a proprietary agent to install and maintain, metrics locked into one backend, and — because every other system needs its *own* agent — a patchwork of tools that don't share a data model.

OpenTelemetry takes the opposite approach. The OpenTelemetry Collector has a **RabbitMQ receiver** built in. You configure one standard pipeline, and because OTLP is an open standard implemented across many tools and backends, the same configuration and the same data work with whatever you point it at. Switch backends without re-instrumenting. Self-host the Collector so your telemetry never leaves your environment. Use the *same* Collector for RabbitMQ, your database, your services, and everything else — one data model, not ten.

In short: the proprietary agent monitors RabbitMQ for *one* tool. The OpenTelemetry receiver monitors it for *any* tool that speaks OTLP — which is most of them now.

## Prerequisites

The RabbitMQ receiver reads from the **RabbitMQ Management Plugin**, so enable it and create a monitoring user:

```bash
rabbitmq-plugins enable rabbitmq_management
# create a least-privilege monitoring user (monitoring tag)
rabbitmqctl add_user otel_monitor 'a-strong-password'
rabbitmqctl set_user_tags otel_monitor monitoring
```

You'll also need an OpenTelemetry Collector build that includes contrib receivers (e.g. `otelcol-contrib`).

## The Collector configuration

```yaml
receivers:
  rabbitmq:
    endpoint: http://localhost:15672      # Management Plugin endpoint
    username: otel_monitor
    password: ${env:RABBITMQ_MONITORING_PASSWORD}
    collection_interval: 60s

exporters:
  otlphttp:                                          # OTLP/HTTP + protobuf (Sluicio ingest)
    endpoint: https://your-tenant-ingest.sluicio.com # base URL; the exporter appends /v1/metrics
    headers:
      authorization: "Bearer ${env:SLUICIO_INGEST_TOKEN}"

service:
  pipelines:
    metrics:
      receivers: [rabbitmq]
      exporters: [otlphttp]
```

That's the whole pipeline: scrape the Management API every 10 seconds, export over OTLP. Point the exporter at any OTLP-compatible backend.

> The RabbitMQ receiver is currently a beta component, so the exact metric set and field names evolve — check the receiver's `documentation.md` / `metadata.yaml` in collector-contrib for the current list before you build dashboards on specific names.

## What to actually watch — and the exact metric

Every queue-level metric is tagged with the resource attributes `rabbitmq.queue.name`, `rabbitmq.node.name`, and `rabbitmq.vhost.name`, so you can break any of them down per queue. These queue-level metrics are **enabled by default**:

| What you're watching | Metric | How to read it |
|---|---|---|
| Backlog / queue depth | `rabbitmq.message.current` where `state=ready` | Messages waiting for a consumer. A steady climb means consumers can't keep up. |
| In-flight / stuck work | `rabbitmq.message.current` where `state=unacknowledged` | Delivered but not yet acked. A rising unacked count usually means a consumer is stuck mid-processing. |
| Consumer presence | `rabbitmq.consumer.count` | Consumers currently reading the queue. **Zero on a queue that should have consumers is a silent outage** — messages arrive and nobody drains them. |
| Throughput in | `rabbitmq.message.published` | Monotonic counter — take the rate. How fast messages are arriving. |
| Throughput out | `rabbitmq.message.delivered` and `rabbitmq.message.acknowledged` | Rates of delivery and acknowledgement. If the published rate outpaces these for any sustained period, you're falling behind. |
| Unroutable messages | `rabbitmq.message.dropped` | Messages dropped as unroutable — almost always a binding / routing-key problem. |

A key detail: `rabbitmq.message.current` carries a `state` attribute with values `ready` and `unacknowledged`. So "queue depth" and "unacked work" aren't two separate metrics — they're the *same* metric filtered by `state`. Don't go hunting for a second metric name.

### Node health is disabled by default — opt in

The node-level metrics ship **disabled**, and that includes the alarm signals that matter most. You have to turn them on explicitly:

```yaml
receivers:
  rabbitmq:
    endpoint: http://localhost:15672
    username: otel_monitor
    password: ${env:RABBITMQ_MONITORING_PASSWORD}
    collection_interval: 60s
    metrics:
      rabbitmq.node.mem_alarm:
        enabled: true
      rabbitmq.node.disk_free_alarm:
        enabled: true
      rabbitmq.node.fd_used:
        enabled: true
      rabbitmq.node.sockets_used:
        enabled: true
```

| What you're watching | Metric | How to read it |
|---|---|---|
| Memory alarm | `rabbitmq.node.mem_alarm` | Trips when RabbitMQ blocks publishers due to memory pressure — a direct "broker in trouble" signal. |
| Disk alarm | `rabbitmq.node.disk_free_alarm` | Trips when free disk drops below the limit and publishers are blocked. |
| File descriptors | `rabbitmq.node.fd_used` vs `rabbitmq.node.fd_total` | Exhausting file descriptors blocks new connections. |
| Sockets | `rabbitmq.node.sockets_used` vs `rabbitmq.node.sockets_total` | Same story for socket exhaustion. |

The single most useful alert for most estates: **`rabbitmq.consumer.count == 0` on a queue that should always have a consumer.** It catches the silent outage that service-level monitoring never sees.

For even higher-resolution message-rate metrics, you can additionally enable the `rabbitmq_prometheus` plugin and scrape it with the Collector's `prometheusreceiver` — still fully OpenTelemetry-native, just a second receiver in the same pipeline.

## The point

Monitoring RabbitMQ is table stakes. Doing it through OpenTelemetry means you do it *once*, in a portable, vendor-neutral way, and the broker's health lands in the same place as the rest of your integration estate — so you can finally see the queue as part of the flow, not as an isolated box.

---

*Notes for Robert: light Sluicio mention only — the exporter comment + the closing line. Verify current metric names against collector-contrib `documentation.md` before publishing (receiver is beta). Optional LinkedIn teaser: one-liner + link to this post.*
