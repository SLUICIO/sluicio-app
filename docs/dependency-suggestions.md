# Service dependency suggestions

When a user builds an integration by pinning specific services (via
`equals` matchers), the UI proposes additional services to include
based on the **trace graph**: who calls into the pinned service, and
who it calls in turn. Each accepted suggestion becomes another
`equals` matcher on the integration.

The goal is to reduce a common manual step — "I added document-intake;
now what else is part of that flow?" — by reusing the trace topology
the product already collects. The suggestions are a hint, not a
prescription: nothing is added until the user confirms.

## API

```
GET /api/v1/services/{name}/neighbors?range=1h
```

Returns the focal service's direct neighbors over the requested
window, grouped by direction:

```json
{
  "service_name": "document-intake",
  "window": { "from": "...", "to": "..." },
  "upstream":   [{ "service_name": "api-gateway", "trace_count": 50, "error_count": 2 }],
  "downstream": [{ "service_name": "ocr",          "trace_count": 40, "error_count": 1 },
                 { "service_name": "classifier",   "trace_count": 30, "error_count": 0 }]
}
```

* **Upstream** — services that called the focal service (parent side
  of a span hop where the focal service was the child).
* **Downstream** — services the focal service called (child side of a
  span hop where the focal service was the parent).
* **Counts** are at trace granularity, matching `FlowEdge`. A single
  trace that crosses the boundary multiple times counts once;
  `error_count` is the subset whose hop was an error on either side.
* Both arrays are pre-sorted by `trace_count` descending and may be
  empty (orphan service, leaf, or quiet window).

The handler is `serviceNeighbors` in `handlers_neighbors.go`. The SQL
lives in `store.ServiceNeighbors` and is a single self-join on the
`traces` table that pivots the focal service into either the parent
or child side via an `if()` expression. Both halves of the join are
bounded by the time range so ClickHouse can prune partitions on each
side, mirroring `ServiceEdges`.

The endpoint **does not** consult any integration's matcher list —
"is this neighbor already covered" is the caller's question, and on
the new-integration page there is no integration to ask about yet.
Returning the raw neighborhood keeps the endpoint composable for
future uses (e.g. dependency search, alert scoping).

## UX

`ServiceDependencySuggestions` renders one panel per focal service.
It fetches `/neighbors`, filters out services the caller marks as
already-covered, and lists upstream + downstream neighbors as two
checkable columns with trace counts. The user picks any subset and
hits "Add" (or "Include in this integration" on the new-integration
form); the chosen names are returned via `onAdd(names[])`.

Two wiring points:

**IntegrationNew** — focal services are the draft form's `equals`
matchers whose value matches a known service. Accepted suggestions
become additional draft matchers; they're persisted only when the
user submits the form. `alreadyCovered` is the list of equals values
in the draft.

**IntegrationDetail** — focal services are the integration's existing
`equals` matchers whose value matches a known service. Accepted
suggestions are POSTed individually as new matchers via
`api.addMatcher`. `alreadyCovered` is the integration's already-
resolved `services[]`, which transitively covers prefix/contains/
regex matchers too.

## Decisions

**Both directions, equal weight.** Upstream and downstream are
shown side-by-side. We considered defaulting to downstream only
("services I call") but the product is about end-to-end flows;
the caller is just as much "part of the flow" as the callee.

**One hop, manual expansion.** Neighbors are direct only. Trans-
itive expansion would surface dozens of services for any non-trivial
graph; the user can accept a neighbor and let the new focal service
surface its own neighbors. This keeps each decision a small step
the user can reason about.

**No threshold filtering.** Every neighbor is shown, regardless of
trace count. The count is rendered next to each row so users can
deprioritize visually. A noise floor would hide real-but-rare
dependencies (cron-driven, edge cases) which are often the ones
people forget to include.

**Same window as the page.** The suggestions track the active time
window from `useTimeWindow`. A quiet window can yield zero neighbors;
the empty-state copy says so explicitly ("try a wider time range, or
this service may be a true leaf") so users distinguish "no data" from
"loading failed."

**Equals coverage on IntegrationNew.** The draft form considers a
service covered if it appears as the value of an `equals` matcher.
Prefix/contains/regex matchers are NOT expanded against the known-
services list there — duplicating the backend matcher logic in
TypeScript would risk drift and falsely promise precision (services
matching a regex but absent from traces in the current window
wouldn't be "covered" by either side's accounting). The detail page
uses the already-resolved `services[]` and so handles all operators
correctly without that compromise.

## Edge cases handled

* **Cycle in the graph.** A worker that both pulls work from and
  reports back to the focal service appears in both upstream and
  downstream. The store query is grouped by `(direction, service_name)`
  so duplicates within one direction collapse; the helper
  `groupNeighborRows` is defensive and sums anyway.
* **Self-hops.** The SQL drops `child.ServiceName != parent.ServiceName`,
  so a service that calls itself never appears in its own neighbor
  list.
* **Empty focal service param.** The handler returns 400; the store
  function returns an empty result with no SQL fired.
* **Empty window.** Both lists empty; the UI explains it (orphan or
  quiet window) and offers no action.
* **Already-covered everything.** If all neighbors are already
  covered, the panel says "every direct neighbor of this service is
  already covered by an existing matcher." This distinguishes from
  "no data."

## Follow-ups

* **Transitive expansion.** A "show 2-hop neighbors" toggle on the
  panel would let users explore further without forcing N round trips.
  Would need a `?depth=2` query param and a graph traversal in the
  store layer.
* **Server-side coverage flag.** If the suggestions endpoint took an
  optional `?integration_id=…`, it could mark each neighbor with
  `already_covered: bool`, removing the client-side filtering and
  letting non-equals matchers participate in coverage on the new-
  integration page too (currently a limitation — see "Decisions").
* **Tests.** Only `groupNeighborRows` is currently unit-tested —
  the repo has no broader test infrastructure yet. A ClickHouse
  test harness covering `store.ServiceNeighbors` would catch SQL
  regressions; a React Testing Library setup would let the component
  be tested in isolation.
* **Threshold opt-in.** While we show all neighbors by default, a
  "hide single-trace neighbors" toggle would help on noisy graphs.
