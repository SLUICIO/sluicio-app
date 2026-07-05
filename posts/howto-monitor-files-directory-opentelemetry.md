# How to monitor files in a directory using OpenTelemetry

**Pillar:** How-To & Use-Cases · **Channel:** Sluicio blog (SEO) + optional LinkedIn teaser
**Angle:** standards-first — why you do this with OpenTelemetry rather than a bespoke script or proprietary agent

**Slug:** /blog/monitor-files-directory-opentelemetry
**Meta description:** File-drop and EDI integrations live and die by what lands in a folder. Here's how to monitor files in a directory with OpenTelemetry using the file stats receiver — vendor-neutral, in the same pipeline as the rest of your estate.
**Target keywords:** monitor files in a directory OpenTelemetry, filestatsreceiver, file-based integration monitoring, hot folder monitoring OTel

---

## The most overlooked integration in your estate is a folder

A huge amount of real-world integration still happens through files. A partner drops an EDI, CSV, or XML file into a hot folder; a scheduled job picks it up and processes it. It's unglamorous and it's everywhere — finance, logistics, healthcare, manufacturing.

It also fails in ways nothing else catches. Files stop arriving (the upstream sender broke). Files pile up (the downstream consumer is stuck). A file sits untouched for an hour (the job that should have grabbed it didn't). A zero-byte file shows up (a transfer half-failed). None of that trips a normal service health check, because the "service" is a directory.

## Why OpenTelemetry — not a cron script or a proprietary agent

Most teams monitor folders with a hand-rolled script: a cron job that counts files and emails someone when the number looks wrong. It's brittle, it lives on one host, and its output goes nowhere useful — certainly not next to the rest of your telemetry. The alternative people reach for is a proprietary monitoring agent, which means yet another vendor-specific thing to install and another data silo.

OpenTelemetry gives you a clean, standard option. The Collector's **file stats receiver** emits metrics about files matching a glob pattern — count, size, modification time — on whatever interval you choose. Because it's OpenTelemetry, those file metrics flow through the *same* pipeline and into the *same* backend as your RabbitMQ, your database, and your services. You can finally correlate "files are piling up in the inbound folder" with "the consumer service started erroring at 14:05." And since OTLP is a standard implemented across many tools, the config is portable and the data isn't trapped anywhere.

## Prerequisites

An OpenTelemetry Collector build that includes contrib receivers (e.g. `otelcol-contrib`), running somewhere that can see the directory (the file server, a sidecar, or a host with the share mounted).

## The Collector configuration

```yaml
receivers:
  filestats:
    include: /data/inbound/partner-edi/*.xml   # glob for the files you care about
    collection_interval: 60s

exporters:
  otlphttp:                                          # OTLP/HTTP + protobuf (Sluicio ingest)
    endpoint: https://your-tenant-ingest.sluicio.com # base URL; the exporter appends /v1/metrics
    headers:
      authorization: "Bearer ${env:SLUICIO_INGEST_TOKEN}"

service:
  pipelines:
    metrics:
      receivers: [filestats]
      exporters: [otlphttp]
```

`include` is the only required setting — point it at the folder and pattern you care about. A 60-second `collection_interval` (also the receiver's default) is plenty for file-drop flows; there's rarely a reason to poll a directory more often than that.

> Check the file stats receiver's `documentation.md` in collector-contrib for the exact metric names available in your Collector version before building alerts on them.

## What to actually watch (the integration lens)

- **File count in the inbound folder** — a rising count means files are arriving faster than they're being consumed, or consumption has stopped entirely. This is your backlog signal.
- **Age of the oldest file (modification time)** — a file that's been sitting longer than your processing SLA means something downstream isn't picking it up. Inversely, if the *newest* file is too old, the upstream sender has gone quiet.
- **File size** — zero-byte or unexpectedly tiny files usually mean a broken or partial transfer.

Two alerts cover most file-based integrations: "inbound count above N" (backlog) and "oldest file older than X minutes" (stuck or starved).

## Files vs. file contents

The file stats receiver answers *"is the file there, and how old is it?"* If you also need *"what's inside the file"* — parsing processing logs or capturing errors written to a file — pair it with the Collector's **file log receiver**, which reads file lines as log records. The two together give you both the shape of the folder and the content of what's flowing through it, all in one OpenTelemetry pipeline.

## The point

A folder is an integration too. Monitoring it with OpenTelemetry turns "files in a directory" into a first-class signal you can alert on and correlate — in the same vendor-neutral pipeline as everything else — instead of a blind spot guarded by a fragile script.

---

*Notes for Robert: light Sluicio mention only. Confirm current filestats metric names against collector-contrib `documentation.md` before publishing.*
