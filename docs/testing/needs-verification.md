<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Needs verification

Features that have **shipped but never been walked by a human on a real
instance**. This is a working list, not a permanent catalog: when a case
here passes, move it into the matching [area protocol](protocols/) and
delete it from this file.

Everything listed here is tracked by the
[`needs-verification`](https://github.com/SLUICIO/sluicio-app/labels/needs-verification)
label, so the issue list and this file can be reconciled at a glance.

## Why this exists separately from the protocols

[`protocols/`](protocols/) is organised by **feature area** and is
permanent: the same cases get walked before every release. This file is
organised by **what just landed** and is temporary.

The distinction matters because the two answer different questions. A
protocol answers "does the product still work". This answers "has anyone
ever actually seen this work". A feature that passes its unit tests, its
typecheck and its lint has been shown to do what its author thought it
should; it has not been shown to do what the customer needs, and on a
green CI run those two are indistinguishable.

## How to walk a case

1. Read **What shipped** so you know what you are looking at.
2. Read **Where** so you know which screen or endpoint to open.
3. Run the steps against a **real instance**, not the dev cell, wherever
   the case says so. Several of these could not be verified locally
   precisely because the dev cell lacks the data that makes them
   meaningful.
4. On a pass, move the case into its area protocol and remove it here.
   On a fail, open a bug and link the issue.

Local stack: `make dev-up`. Sign-in for the dev cell is in the session
notes, not here.

---

## #12 — Error attribution by attribute, not by service

**What shipped.** An integration's error breakdown no longer always
splits by service. Several member services still split by service; a
single member service splits by the defining attribute when it takes
more than one value, and otherwise by the operation that failed. The
view states which split it is showing.

**Where.** Integration overview, the "Where are the error traces?"
panel. API: `GET /api/v1/integrations/{id}/error-breakdown`.

**Verified so far.** The span split, on a temporary integration over
`erp-connector` in the dev cell.

**Not verified.** The **attribute** split, which needs an integration
whose defining attribute matches several values — a Node-RED runtime
matched by a regex or an `in` list rather than a single flow. macmini01
has the data; the dev cell does not.

**Fixture.** [`fixtures/nodered-issue12.json`](fixtures/nodered-issue12.json)
imports two Node-RED flows that fail on a fraction of runs, at
differently-named steps. Their tab ids are FIXED (`sluiciotest0a`,
`sluiciotest0b`) so one integration can match both:

```
service.name  equals   <your node-red service>
node_red.flow.id  matches   ^sluiciotest0
```

Both functions `throw` rather than calling `node.error`. That is not
style: `msg.error` alone routes to `catch` without firing `onComplete`,
so the span is never marked failed and the fixture would produce no
error traces at all.

| | |
|--|--|
| **Case 12.1** | On an integration spanning several services, the breakdown still splits by service and reads as before. |
| **Case 12.2** | On a single-service integration whose attribute matches several values, rows are the attribute's values and the subtitle names the attribute. |
| **Case 12.3** | On a single-service integration with a pinned attribute, rows are operation names, not one row for the runtime. |
| **Case 12.4** | The failing-trace count above the rows matches the integration header's error count, including after acknowledging errors. This was contradictory before; check both numbers agree. |
| **Case 12.5** | The rows deliberately sum to more than the trace count when a trace failed at several operations, and the line under them says so. Confirm no percentage is shown. |

---

## #14 — Cell health

**What shipped.** `/readyz` (dependency reachability, 503 when not
ready), `GET /api/v1/cell-health` (dependencies plus every background
loop's last completed cycle), and a heartbeat on ten loops.

**Where.** Endpoints only; there is no page yet.

**Verified so far.** All three endpoints on a real cell (macmini01):
dependencies answer, the token gate holds (401 without, 200 with), ten
loops report, and a freshly restarted cell reports `unknown` rather than
`stale` — including the useful detail that a slow first cycle can leave
a 30s loop `unknown` for a minute after boot without that being a fault.
No organisation names or per-org figures appear in the payload.

**Not verified.** A loop actually going **stale**, which is the case the
feature exists for and cannot be produced without breaking something on
purpose.

**Not built.** Capacity reporting. #14's design says capacity is
reported but not judged; none of it shipped, so Case 14.7 below cannot
be walked — there is nothing to check. Tracked in #21 rather than left
looking like a passed case.

| | |
|--|--|
| **Case 14.1** | `GET /healthz` still returns `{"status":"ok"}` unauthenticated. |
| **Case 14.2** | `GET /readyz` returns 200 and an empty `unavailable` list on a healthy cell. |
| **Case 14.3** | Stop ClickHouse. `/readyz` returns **503** and names it; `/api/v1/cell-health` reports it down with the driver's error. Restart and confirm both recover. |
| **Case 14.4** | `GET /api/v1/cell-health` without `SLUICIO_HEALTH_TOKEN` set returns 401 even with a correct-looking bearer token. |
| **Case 14.5** | With the token set, the report lists ten loops. After the cell has run a few minutes, the frequent ones (`alerting`, `demand-writer`, `event-subscriptions`) read `ok`. |
| **Case 14.6** | A response contains **no organisation names and no per-org figures**. This is a privacy boundary, not a formatting preference: a cell operator and a tenant's admins can be different parties. |
| **Case 14.7** | ~~Capacity never moves `status`.~~ **Not testable: capacity is not reported at all. See #21.** |

---

## #10 — Proposed integrations from the call graph

**What shipped.** Candidate groupings derived from observed calls,
duplicate suppression for create proposals, drift semantics for a
create, and two MCP tools.

**Where.** `GET /api/v1/integration-candidates`. MCP:
`sluicio_integration_candidates`, `sluicio_list_proposals`.

**Verified so far.** Candidates on the dev cell: two groupings out of 38
unassigned services, both correct.

**Not verified.** The create-proposal path end to end — filing one,
seeing it in the inbox, approving it, and getting a real integration.
There is no UI for a create proposal yet, so the inbox card renders a
`before`/`after` diff shape that a create does not have.

| | |
|--|--|
| **Case 10.1** | On an instance with unassigned services, candidates are groupings a human would recognise, and services already in an integration never appear. |
| **Case 10.2** | An oversized component appears under `skipped_oversized` rather than vanishing. |
| **Case 10.3** | File the same create proposal twice; the second is refused as a duplicate while the first is pending. |
| **Case 10.4** | Reject the first, then file it again. It is accepted, because rejecting has never meant "never again". |
| **Case 10.5** | `sluicio_list_proposals` returns the queue with states, and an agent can distinguish pending from rejected. |

---

## #19 — Span links and the Steps graph

**What shipped.** Span links stored at ingest (capped at 32, true count
kept) and returned with a trace; a Steps view drawing the trace as a
folded graph.

**Where.** Trace full view, the Services / **Steps** toggle.

**Verified so far.** A link surviving OTLP → ClickHouse → API; a
76-span trace folding to 4 steps marked ×19; a linked trace reporting
one hand-off; an older trace reporting hand-offs as unknown.

**Not verified.** A **real** asynchronous hand-off from a Node-RED
`delay` or `catch` retry, rather than a hand-crafted probe span. That
needs macmini01 with the newer library.

| | |
|--|--|
| **Case 19.1** | On a real Node-RED flow using `delay` or a `catch` retry, the consumer trace shows a hand-off and the linked trace id is the producer's. |
| **Case 19.2** | A trace ingested before v0.11.91 says its hand-offs are **unknown**, not "none". |
| **Case 19.3** | A wide trace (dozens of distinct step names) declines to draw, explains why in terms of the producer, and points at the waterfall. |
| **Case 19.4** | Folded steps read `overlapping` or `in sequence`, never "parallel". |
| **Case 19.5** | Clicking a step selects the same span the waterfall selects, and the reverse. |

---

## #15 — Where is this message right now

**What shipped in v0.11.83.** A single trace projected onto the
integration flow graph.

**Not verified.** The **"where is it?" link on a firing completion
alert**, on the integration's Errors tab. The dev cell has no open
completion firings, so the row has never been rendered. Everything else
in #15 was verified.

| | |
|--|--|
| **Case 15.1** | On an instance with a delayed message breaching a completion SLA, the Errors tab lists the firing with a "where is it?" link beside it. |
| **Case 15.2** | The link opens the integration flow with that message projected: the last reached service, and the next expected one named. |
