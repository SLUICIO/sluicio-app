<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Telemetry Advisor & Alert Fatigue Advisor (design)

Status: **shipped** (2026-07-29, issue #1). All six open questions were
settled 2026-07-27 — see §8. Implementation notes where the code
departs from or sharpens this document are inline in
`services/cell-api/internal/advisor/`.

Two advisors on one shared foundation, both applying the same idea:
**Sluicio is the only consumer surface for the telemetry it stores, so it
can measure what is actually *used* — and recommend cutting what isn't.**

1. **Telemetry Advisor** — joins ingest volume against observed demand
   (rules, facets, dashboards, human exploration) and suggests OTel
   collector changes for telemetry with no consumer, each with a
   ready-to-paste collector snippet. Suggests, never applies.
2. **Alert Fatigue Advisor** — joins alert-rule firing history against
   operator interaction (acks, handled marks, deep-link click-throughs)
   and suggests tuning rules nobody acts on.

Design principle, from the conversation that spawned this: integration
monitoring is a **completeness** business — every message matters. So
the advisor must never suggest sampling or dropping integration
*messages*. The savings live in attributes, logs, metrics, and noise.

A second principle: **deterministic core, optional AI garnish.** Every
suggestion must be derivable (and explainable) from counted facts. An
LLM may later rank suggestions and write friendlier rationales; it must
never be the reason a suggestion exists.

---

## 1. What this is / is not

| | |
|---|---|
| **Is** | An advisory report: "this telemetry/alerting costs you X and nobody consumes it — here is the exact change to stop paying." Accept/dismiss workflow, audited. |
| **Is not** | Remote collector management. v1 emits copy-paste snippets exactly like the existing per-item Trim panels (`LogTrimPanel`, `TraceTrimPanel`) — the operator stays in control of their collector. (OpAMP-based push is sketched as a possible v3, decision deferred.) |
| **Is not** | Anomaly detection, forecasting, or auto-tuning. No thresholds move without a human accepting a suggestion. |

---

## 2. Shared foundation: the demand ledger

A small ClickHouse table recording *consumption* of telemetry, written
by cell-api (the single chokepoint every read already flows through):

```
telemetry_demand
  Day            Date            -- aggregation grain: daily
  OrganizationID String
  Signal         LowCardinality  -- trace | log | metric
  ServiceName    String          -- "" when not service-scoped
  Key            String          -- metric name, attribute key, or "" (whole-signal)
  ConsumerKind   LowCardinality  -- human | rule | facet | matcher | dashboard | completion | template
  Hits           UInt64          -- SummingMergeTree accumulates
ENGINE SummingMergeTree ORDER BY (OrganizationID, Day, Signal, ServiceName, Key, ConsumerKind)
```

Two writer categories:

- **Human demand** (new instrumentation, ~15 handler touchpoints):
  log/metric/trace explorer queries record the service + attribute keys
  + metric names they filtered or charted; opening a log drawer or
  trace records the service; Usage/ServiceDetail views record their
  scope. Buffered in-process and flushed in batches (same pattern as
  span inserts) — one counter bump per query, no latency on the read
  path.
- **Mechanical demand** (no instrumentation needed): a nightly sweep
  walks org config — alert rules (metric names, log/trace attr
  predicates, split_by), integration matchers (attribute keys), facet
  mappings + key attributes, dashboards, completion rules, monitoring
  templates — and writes one `Hits=1` row per referenced key per day.
  Config *is* demand, permanently, for as long as the reference exists.

**Privacy: aggregate-only.** No user id, no query text, no row-level
"who looked at what" — daily counters per org. This is deliberate
(EU/works-council reality) and also sufficient: the advisor needs "was
this consumed", never "by whom". The ledger must be documented in
docs/security.md when shipped.

Retention: 400 days TTL (covers "unused for a year" reasoning), a few
MB/org/year at daily grain. Not exported by config transfer (it is
observed fact, not configuration).

Supply-side needs no new table: per-(service, signal) row/byte volume
is already answerable from the telemetry tables (the Usage page does
it); the advisor adds per-key volume queries (attribute bytes via
sampled `mapKeys`/length aggregates, per-metric row counts).

---

## 3. Telemetry Advisor

### 3.1 Suggestion classes (v1)

Each class = supply query + demand join + guardrails + snippet template.

| # | Class | Finding | Suggested change (collector) |
|---|---|---|---|
| T1 | Unused metric | Metric ingested ≥30d, zero demand rows in 30d | `filter` processor dropping the metric (or widen scrape interval — text hint) |
| T2 | Unviewed log stream | (service, severity band) log volume with zero demand | `filter/severity_number` floor for that service |
| T3 | Dead-weight span attribute | Attribute present on ≥X% of a service's spans, ≥Y bytes/span, zero demand | `transform` processor deleting the attribute |
| T4 | High-cardinality attribute | Distinct values > threshold (attribute catalog already computes this), zero rule/facet demand | `transform` delete or hash — flagged as *review*, never auto-worded as safe |
| T5 | Header/payload echo | `http.request.header.*` / `*.body`-shaped keys with zero demand (the KrakenD `report_headers` case) | `transform` delete with key regex |
| T6 | PII-shaped values | Attribute values matching email/IBAN/personal-number patterns (sampled scan) | `redaction` processor snippet; severity "compliance", shown regardless of demand |

Explicitly **out of scope forever** (product promise): suggestions to
sample or drop spans/traces on services that are members of any
integration, and anything referenced by *any* mechanical consumer.

### 3.2 Guardrails (hard, not heuristics)

- **Any demand = no suggestion.** One human view in the window kills it.
- **Mechanical references veto regardless of window** — a metric used
  by a paused alert rule is still in use.
- **Quarantine**: telemetry younger than the observation window is
  never judged (default 30 days — open question #3).
- **Loss statement**: every suggestion renders "what you lose" ("the
  `/logs` DEBUG view for warehouse-sync will be empty") next to "what
  you save" (rows/bytes/day, % of org ingest).
- Suggestions are **idempotent and fingerprinted** (class + scope +
  key): dismissing one keeps it dismissed until the underlying facts
  change materially (volume doubles, demand appears).

### 3.3 Lifecycle & audit

`advisor_suggestions` (Postgres, per-org): fingerprint, class, scope,
evidence JSON (volumes, last-demand dates), snippet, state
(`open | accepted | dismissed`), acted-by/at. Accepting records an
audit entry (`advisor.suggestion_accepted`) — the suggestion text is
the operator's paper trail for "why did we drop this attribute".
Accepted ≠ applied: v1 cannot know the collector actually changed; the
nightly job flips accepted suggestions to `verified` when supply
actually drops (nice signal, zero extra plumbing).

---

## 4. Alert Fatigue Advisor

Interaction signals that already exist, per rule over a window:

- instance lifecycle: fire count, mean duration, `last_notified_at`
  renotify churn (alert_instances)
- **click-throughs**: alert deep-links carry `?instance=` (shipped
  v0.11.9) — the handler records a demand row when a deep-link is
  opened, turning email/Slack engagement into data
- acks: `resolve_mode=ack` acknowledgments, per-service error-ack
  watermarks (0030), completion-firing `handled_at`
- digest views (digest-seen already tracked)

Suggestion classes:

| # | Class | Finding | Suggestion |
|---|---|---|---|
| F1 | Ignored rule | ≥N firings in 30d, zero interactions | Raise threshold, reduce severity, or disable — operator picks, and **the advisor suggests no number** (see below) |
| F2 | Wallpaper | One instance firing continuously ≥14d | "This is a state, not an alert" — fix or disable |
| F3 | Flapper | ≥N fire→resolve cycles/day | Widen window / add hysteresis (suggested values from the observed cycle period) |
| F4 | Channel-less | Enabled rule with no channel and no UI views | Route it or drop it |
| F5 | Duplicate | Two rules on the same scope firing within the same minutes ≥90% of the time | Merge candidates |

Same lifecycle table, same accept/dismiss/audit, same fingerprinting.

**F1 puts no number in the action** (decided 2026-07-27). The advisor
shows what was observed — how often the rule fired, how often anyone
engaged — and lets the operator choose the new threshold. A suggested
number in a one-click button is how someone ends up silencing a real
alert on our recommendation, and the first time that happens they stop
trusting every other suggestion too.

A related trap in the statistics themselves: **a percentile is only
meaningful for a distribution-shaped metric** — response times, queue
depth, latency. It is the wrong summary for anything counted per
integration message (message counts, error counts, completeness), where
every message is significant and a quantile silently frames away the
tail that matters. Where evidence is shown for a message-shaped rule, it
must be counts and frequencies, never a quantile.

---

## 5. Surfaces

- **Advisor page** (proposed: nav under Settings-adjacent operations,
  final placement open question #4): two tabs (Telemetry, Alerting),
  ranked by savings/noise, each card = finding → evidence → loss
  statement → snippet (copy button) → accept/dismiss.
- **Digest**: one line when new suggestions appeared since last visit.
- **API**: `GET /api/v1/advisor/suggestions`,
  `POST /api/v1/advisor/suggestions/{id}/accept|dismiss`; evaluation
  runs nightly plus `POST /api/v1/advisor/run` (admin, rate-limited)
  for demos.
- **RBAC**: viewing = admin only (it reveals org-wide cost/usage);
  accept/dismiss = admin. Group-scoped editors don't see it (v1).
- **MCP**: expose `sluicio_advisor_suggestions` so the report is
  scriptable/chat-consumable for free.

---

## 6. The optional LLM layer (explicitly later, v2+)

Slot, not dependency: a `rationale` field per suggestion, rendered from
deterministic templates in v1. v2 may fill it via a BYO
OpenAI-compatible endpoint (cell setting, secret encrypted at rest like
SMTP) for: friendlier grouped narratives ("these 14 attributes look
like one debugging session"), cluster naming, cross-suggestion ranking.
Constraints if/when added: read-only inputs (the evidence JSON, never
raw telemetry bodies unless the operator opts in), no tool use, output
is display-text only — an LLM cannot create, score, or auto-accept a
suggestion.

---

## 7. Phasing

- **P1** Demand ledger (mechanical sweep + human touchpoints) — invisible, ships alone, starts accumulating history.
- **P2** Telemetry Advisor T1–T3 + lifecycle + page + snippets (T4/T5 fast follows; T6 separable).
- **P3** Alert Fatigue Advisor F1–F4 (F5 later; needs co-firing analysis).
- **v2+** LLM rationales; novelty-detection digest (separate design); OpAMP push (separate design, big trust decision).

P2 is demo-ready ~30 days after P1 deploys (quarantine window) — the
dev/demo cells can seed sooner with a shortened window for screenshots.

---

## 8. Decisions (settled 2026-07-27)

1. **CE or EE?** Demand ledger + the Usage-page "last consumed"
   enrichment are **CE**; both advisors sit behind a new **EE
   entitlement `advisor`** — the 6th. Splitting the two advisors across
   editions was rejected: two code paths for one feature, and no
   coherent story for why one advisor is free.
2. **Human-demand tracking**: **aggregate-only, no toggle.** There is no
   personal data to opt out of, and an opt-out would mostly produce
   silent "the advisor says nothing" support cases months later. Purely
   additive if a customer ever needs it. Documented in docs/security.md.
3. **Observation window**: **30 days, fixed in v1.** Long enough to
   survive a monthly reporting cycle, short enough that P2 is
   demonstrable a month after P1 ships. Revisit if a real cell wants
   longer.
4. **UI placement**: **a tab under Usage.** That is where someone
   already goes to ask what telemetry costs; the advisor answers the
   next question. A top-level nav entry oversells a monthly visit.
5. **Snippet dialect**: **OTel collector processors only.** The one
   surface that is always the operator's to change, and it matches the
   existing Trim panels. Emitter-specific hints multiply templates we
   cannot keep current.
6. **F1 threshold aggressiveness**: **evidence only, no suggested
   number** — see §4 for the reasoning, including why a percentile is
   the wrong statistic for anything counted per integration message.

---

## 9. Implementation notes (2026-07-29)

Two things the design did not anticipate, both found while building.

**The ledger must be as old as the window before anything is judged.**
The absence of demand is only evidence if we were watching. On a cell
whose ledger started last week, a metric somebody charted three weeks
ago has no recorded demand, and every evaluator reads that as "nobody
uses this". The advisors therefore return nothing until the ledger spans
the observation window, and the UI says so — an empty advisor and one
that has not been watching long enough look identical on screen and mean
opposite things. Mechanical demand is exempt: the sweep re-records
config daily, so alert rules, facet mappings and dashboards protect
their telemetry from day one. It is people's queries that need history.

`ADVISOR_WINDOW_DAYS` shortens the window for testing and demos. It is a
cell env var rather than an org setting on purpose: a shorter window
makes the advisor readier to call something unused, and a customer
should not be able to make their own advisor jumpier without realising
that is what they did. The effective window is echoed in every API
response so findings from a shortened window cannot be mistaken for a
month's observation.

**Facet mappings had to become a demand source before T3 could ship.**
An attribute that classifies a service into a facet is consumed by code,
not by any query, so every other demand source is silent about it — and
T3 would have proposed deleting the attribute that makes the service's
own detail page work. Dashboards were added for the same reason at
whole-signal grain: a pinned integration is a standing claim on its
services, and whether anyone filtered an attribute on it is not the
point.
