# How to monitor a SQL Server instance using OpenTelemetry

**Pillar:** How-To & Use-Cases · **Channel:** Sluicio blog (SEO) + optional LinkedIn teaser
**Angle:** standards-first — why you do this with OpenTelemetry rather than a proprietary agent

**Slug:** /blog/monitor-sql-server-opentelemetry
**Meta description:** Most integrations lean on a database, and when SQL Server slows, everything on top of it slows too. Here's how to monitor a SQL Server instance with OpenTelemetry — direct connection or Windows performance counters — in one vendor-neutral pipeline.
**Target keywords:** monitor SQL Server OpenTelemetry, sqlserverreceiver, SQL Server metrics OTel Collector, SQL Server performance monitoring

---

## When the database slows, every integration slows

Almost every integration touches a database somewhere — a staging table, a lookup, a transactional write. SQL Server is one of the most common, and it's a classic hidden cause of integration pain. Lock waits and blocked sessions back up the services on top of it. Low page life expectancy means memory pressure and slow reads. Connection-pool exhaustion — often caused by one misbehaving integration — locks everyone else out. If you only watch the application, you see "everything is slow" with no idea why. The answer is usually in the database.

## Why OpenTelemetry — not a proprietary agent

The usual route is a database-specific monitoring agent that ships SQL Server metrics into one vendor's cloud, in that vendor's format. It works, but it's another proprietary agent to deploy and license, and the data lands in a silo separate from the rest of your telemetry — so you still can't easily line up "the database started blocking" with "the integration started timing out."

OpenTelemetry has a **SQL Server receiver** in the Collector. One standard pipeline collects SQL Server's performance metrics and sends them, over OTLP, into the same backend as your services, your message broker, and your file flows. Because OTLP is an open standard implemented broadly, the configuration is portable and the data is yours — self-host the Collector and nothing about your database leaves your environment. The database stops being a separate monitoring island and becomes part of the integration picture.

## Two ways to connect

The SQL Server receiver works in one of two modes:

1. **Windows performance counters** — run the Collector on the Windows host running SQL Server; it reads the perf counters locally.
2. **Direct connection** — the Collector connects to the instance over the standard database protocol and queries system DMVs. This is cross-platform (you can run the Collector on Linux) and it unlocks additional metrics — locks, memory statistics, file I/O, resource pools — that aren't available from perf counters alone.

For most integration estates the **direct connection** is the better choice: it's portable and gives you the richer metric set.

## Prerequisites

A least-privilege monitoring login on the SQL Server instance, and an OpenTelemetry Collector build with contrib receivers (e.g. `otelcol-contrib`).

## The Collector configuration (direct connection)

```yaml
receivers:
  sqlserver:
    username: otel_monitor
    password: ${env:SQLSERVER_PASSWORD}
    server: 10.0.0.10          # IP or hostname of the instance
    port: 1433
    collection_interval: 30s

exporters:
  otlphttp:                                          # OTLP/HTTP + protobuf (Sluicio ingest)
    endpoint: https://your-tenant-ingest.sluicio.com # base URL; the exporter appends /v1/metrics
    headers:
      authorization: "Bearer ${env:SLUICIO_INGEST_TOKEN}"

service:
  pipelines:
    metrics:
      receivers: [sqlserver]
      exporters: [otlphttp]
```

To enable direct connection you must specify `username`, `password`, `server`, and `port` together (or, for finer control, use a `datasource` connection string instead — but not both).

> Some metrics are only produced in direct-connection mode. Check the SQL Server receiver's `documentation.md` in collector-contrib for the exact metric set in your Collector version.

## What to actually watch (the integration lens)

- **Lock waits / blocked sessions** — the number-one way a database silently throttles the integrations on top of it.
- **Batch requests / sec** — your throughput pulse; a drop alongside rising latency upstream points straight at the DB.
- **Page life expectancy** — a steep, sustained drop signals memory pressure and slower queries.
- **User connections** — watch for a climb toward the pool limit; a leaking or runaway integration shows up here first.
- **File / disk I/O** — slow I/O turns every query into a bottleneck.

## Going beyond infrastructure metrics

The receiver gives you server health. For *business-level* signals — rows waiting in a staging table, the age of the last successfully processed record, a count of failed rows — use the Collector's **SQL query receiver** (`sqlqueryreceiver`) to run your own SQL on an interval and emit the result as metrics or logs. That's how you turn "the database is healthy" into "this specific integration is keeping up," all still inside OpenTelemetry.

## The point

SQL Server health *is* integration health for anything that depends on it. Collecting it through OpenTelemetry — once, in a portable and vendor-neutral way — puts the database in the same pane as the services and queues around it, so the next time an integration mysteriously slows down, the cause is one correlation away instead of a separate tool you have to go open.

---

*Notes for Robert: light Sluicio mention only. Verify current metric names and direct-connection requirements against collector-contrib `documentation.md`/README before publishing.*
