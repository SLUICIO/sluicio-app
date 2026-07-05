# Why continuous telemetry optimization matters (what data should you actually send?)

**Pillar:** Category & Concepts (cost-flavored) · **Channel:** LinkedIn (personal) + Sluicio blog
**Goal:** thought leadership on telemetry hygiene; soft seed of a future Sluicio capability (flagging unused signals)

> Deliberately light on Sluicio — the point is the principle, not the product. Keep the sluicio.com link in the body.

---

## LinkedIn version

The cheapest telemetry is the data you never send.

OpenTelemetry made it trivial to emit everything — every span, every metric, every attribute. So that's what most teams do by default. Then the bill shows up: ingest, storage, cardinality, slower queries — much of it for data nobody ever looks at.

Instrumentation should be rich. What you *export and keep* should be deliberate.

A few questions worth asking on a regular basis:

→ Which metrics has nobody queried in the last 90 days?
→ Which attributes are quietly blowing up your cardinality?
→ What could be sampled, aggregated, or dropped at the Collector instead of stored forever?

And the part people miss: this isn't a one-time cleanup. Services change, new attributes sneak in, yesterday's useful signal becomes today's noise. Telemetry optimization is continuous, or it doesn't happen at all.

Send the signal. Drop the exhaust.

#OpenTelemetry #Observability

---

## Blog version (SEO)

**Title:** What Telemetry Should You Actually Send? The Case for Continuous OpenTelemetry Optimization
**Slug:** /blog/continuous-telemetry-optimization
**Meta description:** OpenTelemetry makes it easy to emit everything — and most of it is never queried. Why deciding what data to actually send (and continuously pruning it) matters for cost, performance, and signal.
**Target keywords:** telemetry cost optimization, OpenTelemetry data volume, metrics cardinality, reduce observability cost, OTel Collector filtering and sampling

### The default with OpenTelemetry is "emit everything"

One of the best things about OpenTelemetry is how easy it makes instrumentation. Auto-instrumentation, sensible defaults, broad library support — point it at your services and telemetry just starts flowing.

That's also the trap. "Easy to emit" quietly becomes "emit everything," and everything is a lot: every span on every request, metrics at full resolution, and a long tail of attributes that came along for the ride. It all flows to your backend, gets stored, and sits there.

The uncomfortable truth is that a large share of it is never queried. It doesn't power a dashboard, doesn't back an alert, and never shows up in an investigation. It's exhaust — and you're paying to move it, store it, and search around it.

### Telemetry isn't free, and the costs compound

Over-sending shows up in more places than the storage line on an invoice:

- **Network and ingest.** Every span and sample is bytes on the wire and work at the receiver. In high-throughput integration estates, telemetry traffic can rival the traffic it's describing.
- **Storage and retention.** Full-resolution data kept for long windows is the single biggest cost driver in most observability setups.
- **Cardinality.** A single high-cardinality attribute — a user ID, a request ID, a raw URL on a metric label — can multiply time series into the millions, inflating storage and degrading query performance well out of proportion to its value.
- **Query speed and signal-to-noise.** More data isn't more insight. Bloated datasets make queries slower and make the actual signal harder to find when you're under pressure during an incident.

### The principle: instrument richly, export deliberately

The goal isn't to instrument less. Rich instrumentation is good — you want the option to see something. The discipline is at the boundary: **decide, deliberately, what leaves the edge and what you persist.**

A useful mental model: instrumentation is the raw feed; the Collector is where you choose what's worth keeping. Practical levers, roughly in order of impact:

1. **Sample traces.** Most requests are unremarkable. Head or tail-based sampling keeps the interesting traces (errors, slow paths) without storing every healthy one.
2. **Drop and aggregate metrics.** If nobody queries a metric, stop exporting it. If you only ever look at it aggregated, aggregate it before it's stored.
3. **Prune attributes.** Strip high-cardinality or low-value attributes at the Collector (filter/transform processors) before they hit the backend.
4. **Tier retention.** Keep high-resolution data for days, downsampled summaries for longer. Not everything deserves the same retention.

### Why it has to be continuous, not a one-time cleanup

Here's the part teams underestimate: a telemetry estate is never "done." You ship a new service and auto-instrumentation adds a fresh batch of metrics. A library upgrade introduces new attributes. A dashboard gets deleted but its metrics keep flowing. What was signal six months ago is noise today.

A one-time audit decays immediately. The only thing that actually controls cost and noise is a **continuous loop**: regularly surface what's unused, what's exploding cardinality, and what could be sampled or dropped — then act on it. Treat telemetry like any other part of the system that drifts and needs maintenance.

### The questions to ask on a cadence

- Which metrics and labels have not been queried in the last 30–90 days?
- Which attributes are driving cardinality, and is that cardinality earning its keep?
- What's being stored at full resolution that only ever gets viewed aggregated?
- What new signals appeared since the last review, and did anyone decide to keep them — or did they just arrive?

### A note on where this is going

Most teams don't do this because it's tedious and invisible — you can't easily *see* which of your thousands of signals nobody uses. That's a problem worth solving: surfacing unused metrics and cardinality offenders so the prune-or-keep decision is obvious. It's something we're starting to build toward at [Sluicio](https://sluicio.com) — but the principle stands whatever you run: **send the signal, drop the exhaust, and keep doing it.**

---

*Notes for Robert:*
- *Light Sluicio mention only (one paragraph) — per brief, this is a principle piece, not a product post.*
- *No invented statistics used; "large share never queried" is stated as observation, not a cited figure. If you want a number, cite a real source (e.g. a vendor cost report) rather than a guess.*
- *Fits as a strong companion to the W26 retention/cost piece — could pair them.*
- *Suggested calendar slot: Positioning & Cost or Category pillar; tell me and I'll add the row.*
