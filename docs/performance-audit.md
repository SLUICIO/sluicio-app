<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# ClickHouse Performance Audit · 2026-05

A pass over the read path against the question: **what breaks when the
spans / logs / metrics tables hold tens of millions of rows?** Findings
are prioritized P0 → P2. P0s ship in the accompanying commit; P1 / P2
are filed as GitHub issues.

## What the schema does well

The three telemetry tables (`traces`, `logs`, `metrics`) all share a
deliberate base layout:

- **Partitioned by day** on `toDate(Timestamp)`. Any time-range query
  prunes whole partitions before reading; a 1h query touches at most
  two partitions regardless of how many millions of rows live in
  older days.
- **ServiceName leads the ORDER BY** on every table, with
  `LowCardinality(String)` encoding. Per-service queries — the
  dominant traffic — hit the sparse index directly.
- **ZSTD + Delta codecs** keep timestamp / map columns compact; a
  million-row partition typically settles to a few hundred MB on disk.
- **Bloom-filter skip indexes** on `mapKeys` / `mapValues` for the
  attribute Maps, and a `tokenbf_v1` on `logs.Body`. These let
  attribute / token searches prune parts without reading them.
- **30-day TTL** caps the steady-state working set.

So the bones are right. The findings below are about specific queries
that don't take advantage of that layout — and a few client-side
defaults that should be tightened before real load hits.

## P0 — ship now

### P0-1 Quadratic Map scan in `DiscoverServiceResourceAttributes`

`store/clickhouse.go:171` (the catalog reconciler's attribute snapshot)

```sql
SELECT ServiceName,
       arrayJoin(mapKeys(ResourceAttributes))   AS key,
       arrayJoin(mapValues(ResourceAttributes)) AS value,
       ResourceAttributes[key] AS expected
FROM traces
WHERE ...
WHERE value = expected
```

Two parallel `arrayJoin`s don't iterate in lockstep — they cartesian-
product. For a map of N entries each row becomes N² rows, only N of
which pass the `value = expected` filter. A service emitting 30
resource attributes does 900 rows of work to keep 30. The reconciler
runs over the whole telemetry window every cycle.

**Fix:** use `ARRAY JOIN` on the Map directly. ClickHouse exposes
`(key, value)` tuples natively:

```sql
SELECT ServiceName, kv.1 AS key, kv.2 AS value
FROM traces
ARRAY JOIN ResourceAttributes AS kv
WHERE ...
GROUP BY ServiceName, key, value
```

One pass, no post-filter, no quadratic blowup.

### P0-2 Unbounded full-table scan in `ListServices`

`store/clickhouse.go:62` (the Services page rollup)

```sql
LEFT JOIN (
    SELECT ServiceName, min(Timestamp) AS FirstSeen
    FROM traces
    GROUP BY ServiceName        -- ← NO time-range filter!
) AS f ON f.ServiceName = s.ServiceName
```

The outer subquery is scoped to the request's window, but the
FirstSeen subquery scans the entire `traces` table every time. At
30 days × millions of rows × every Services page load, this is the
single most expensive read in the codebase.

**Fix (immediate):** bound it to the TTL window (`Timestamp >=
toDate(now()) - INTERVAL 30 DAY`). At the FirstSeen level it's
identical to the unbounded query since TTL drops anything older.

**Fix (proper, later):** source FirstSeen from the Postgres
`service_catalog.first_seen_at` column — already maintained by the
reconciler. Removes the JOIN entirely.

### P0-3 No query timeout / row cap on the ClickHouse client

`pkg/clickhouse/client.go:82` (the connection options)

The driver opens with no `Settings` map. A pathological query (a
ten-day Body substring scan, an unbounded GROUP BY on a high-cardinality
attribute key) can pin a CH worker indefinitely and block other reads.

**Fix:** apply server-side guardrails as default settings on every
query:

```go
clickhouse.Options{
    ...
    Settings: map[string]any{
        "max_execution_time":      30,           // seconds
        "max_rows_to_read":        2_000_000_000, // 2B row hard cap
        "max_bytes_to_read":       50_000_000_000, // 50GB hard cap
        "read_overflow_mode":      "throw",
        "max_memory_usage":        2_000_000_000, // 2GB per query
        "group_by_overflow_mode":  "throw",
    },
}
```

Caller-supplied long-running queries can override with a session ID
later; this is the default floor.

### P0-4 `Body` substring search bypasses the bloom-filter index

`store/clickhouse.go:1335` (the free-text log filter)

```go
where = append(where, "positionCaseInsensitive(Body, ?) > 0")
```

The schema has `INDEX idx_body Body TYPE tokenbf_v1(...) GRANULARITY 1`,
but `positionCaseInsensitive` is an arbitrary substring matcher and
doesn't use the index. Every part's `Body` column is decompressed and
scanned.

**Fix:** when the query is a single whitespace-free token (the common
case), `AND` a `hasTokenCaseInsensitive(Body, ?)` predicate. The bloom
filter prunes parts where the token can't appear; the
`positionCaseInsensitive` still runs but only against the surviving
parts. For multi-token queries the helper falls through to the slow
path (same as today). Net effect: single-keyword body search becomes
near-free at scale, multi-word stays at parity.

### P0-5 `DistinctLogServices` runs on every `?integration=` request

`api/handlers_logs.go:123` calls `Store.DistinctLogServices` to
enumerate the candidate service names for the integrationFilter
intersection. That's a `SELECT DISTINCT ServiceName FROM logs WHERE
Timestamp BETWEEN ?` — scans every part in the window.

We already maintain a Postgres `services` table (the catalog
reconciler keeps it fresh per org). The integration → services
resolution can come from there with zero ClickHouse cost.

**Fix:** swap the `DistinctLogServices` lookup for
`catalog.ListServices(ctx, orgID)`. Catalog is org-scoped and
authoritative; CH is just the live signal feed.

## P1 — file as issues

### P1-1 Log table sort key uses `SeverityText`, but queries filter on `SeverityNumber`

`pkg/clickhouse/migrations/0002_logs.sql`:

```sql
ORDER BY (ServiceName, SeverityText, toUnixTimestamp(Timestamp))
```

Every severity floor we apply (`SeverityNumber >= 17`) bypasses the
sort key — CH can't translate a numeric range to a discrete
`LowCardinality(String)` lookup. Result: within a service partition,
severity filtering is a sequential scan.

**Fix (requires migration):** either rebuild the table with
`SeverityNumber` as the 2nd sort key, OR add a materialized
`SeverityBand` LowCardinality column (`info`/`warn`/`error`/`fatal`)
and put that in the sort key.

### P1-2 Logs page sends 3 independent queries per state change

`/logs`, `/logs/volume`, `/logs/groups` all run on the same filters
each time the user types into the search box. ClickHouse's query cache
helps once the queries stabilize, but the typing cycle blows cache for
each keystroke (debounced to 250 ms — still ~3 keystrokes/sec * 3
queries = 9 queries/sec for one user).

**Fix:** add a small in-process LRU on the cell-api side, keyed by
`(window, filter-hash)`, with a 5s TTL. Bounds CH load during
interactive filtering.

### P1-3 No GROUP BY cardinality cap on `LogGroups` with `attribute` key

`store/clickhouse.go:1411` — `GROUP BY <attribute-effective-value>`.
For a low-cardinality key like `environment` this is fine. For
`request_id` or `trace_id` (high-cardinality) it groups every distinct
value, sorts by count DESC, then `LIMIT 300`. The sort/dedup over
millions of unique values is the cost — the LIMIT only saves IO at the
output step.

**Fix:** add `max_rows_to_group_by` to the query settings + handle
`group_by_overflow_mode='throw'` with a clear "this attribute has too
many distinct values to group" error.

### P1-4 No result cache for catalog-style listings

`/log-fields`, `/integrations`, `/services`, `/metric-names` are
polled at high rate by the UI (every page mount, every filter open).
They barely change minute-to-minute. They each cost one ClickHouse
query.

**Fix:** wrap with a 30 s TTL Go LRU. ~50 lines of code, eliminates
the chatty load.

## P2 — document, defer

### P2-1 Pre-aggregated rollups via MaterializedView

Volume histograms re-aggregate raw points on every request. A
`logs_per_minute_by_service_severity` MaterializedView would make the
histogram a `LIMIT 60` scan of pre-rolled buckets — sub-millisecond
even at billions of rows. Big project; the bloom filters + partitioning
make it unnecessary until we cross ~100M logs/day.

### P2-2 Row-level ABAC attribute filtering

Today, policy gating narrows the visible `ServiceName IN (...)` set,
not the per-row attribute filter. A user with `team=orders` policy
who shares a service with a `team=payments` engineer sees both teams'
rows. The row-level filter requires injecting per-policy WHERE
clauses into every read.

### P2-3 ORDER BY tweaks per table

The `metrics` ORDER BY puts `MetricName` before `toUnixTimestamp` —
optimal for "metric M for service S over a window," suboptimal for
"all metrics for service S in the last 5 minutes" (the Service Detail
view). Acceptable today.

## Pagination

The other half of "millions of rows" — once the data is there, how do
we navigate it without re-reading the world per page?

### What works today

| Endpoint | Strategy | Why it scales |
|---|---|---|
| `GET /api/v1/logs` | Keyset cursor `(TSNano, LogId)` | LogId is a generated UUID per row → unique tiebreaker even for byte-identical rows at the same timestamp. ClickHouse uses the sort key to skip ahead; cost per page is constant. |
| `POST /api/v1/messages/search` | Keyset cursor `(LatestMatchNano, TraceId)` | Cursor goes in the matching CTE's HAVING, so the LIMIT applies AFTER status / pagination filters — no shrinkage below the page size. |
| `GET /api/v1/logs/volume` | Bucketed, capped at 240 buckets | The bucket width derives from the window. No paging needed. |
| Other read paths | LIMIT only (no cursor), all capped ≤ 1000 | List endpoints (`/services`, `/integrations`, `/log-services`, etc.) have a single page; the cap is the upper bound. |

**No OFFSET anywhere** — I checked. Every paginated query keysets,
which is the right model for ClickHouse (sort key skipping vs. re-read).

### P0 — fixed in this commit

#### P0-6 `SpansForTrace` had no LIMIT

`store/clickhouse.go:868` returned every span for a TraceId, ordered
by start time. The TraceId filter is narrow (bloom-indexed) so CH IO is
cheap, but the JSON payload + the browser's waterfall renderer (which
does **not** virtualize) explode at thousands of spans. A misbehaving
producer in a long-running loop can emit a 50K-span trace that
single-handedly OOMs the tab.

**Fix:** added a `limit int` parameter (default 5000, ceiling 50000),
and the handler fetches `cap+1` so it can flag `truncated=true` on the
response without a separate count query. The `TraceDetail` JSON gains
a `truncated` field; the frontend renders an inline warning banner.

#### P0-7 `ListServices` had no LIMIT

`store/clickhouse.go:62` aggregated all services in the window with no
upper bound. Real orgs have ≤ thousands of services so this hasn't
mattered, but a misconfigured ingest with random `service.name` values
could create one row per request. Added `LIMIT 5000` — five times any
realistic org's service count, ten times what we've ever seen in a
single tenant.

### P1 — filed as issues

#### P1-5 `SearchTraces` (`/api/v1/search`) has no cursor

`handlers.go:917` calls `SearchTraces(q, limit)` with a default of 200,
cap of 1000. Once a search returns ≥ limit, the user can't see results
past it. Should grow a `MessageCursor`-style keyset like
`SearchMessages` already has.

#### P1-6 Group-rollup LIMITs are hard caps, not pages

`LogGroups` and `MetricGroups` both `LIMIT 300` (after ordering by
count desc). Groups 301+ are invisible. For "service" / "severity"
dimensions this is fine (low cardinality). For "attribute" with a
high-cardinality key it can hide real groups behind chunkier ones.

Either:
- Add a cursor based on `(count, key)` — but ranking by count is
  inherently unstable as new data arrives
- Or add a "show all" mode that streams via a separate endpoint with
  no order guarantees

### Pagination ground rules (for future endpoints)

1. **Never use OFFSET on ClickHouse.** It re-reads from the top every
   page. Use a keyset cursor — at minimum `(sort_key, unique_id)`.
2. **The unique_id must really be unique.** A content hash collides;
   use a generated UUID or an autoincrement-style id. Cursor pages
   silently drop rows on collisions otherwise.
3. **Put the cursor in WHERE/HAVING, not in app code.** That's how CH
   uses the sort key to skip; app-level filtering reads + discards.
4. **Cap the LIMIT server-side.** Default sensibly (50–200). Max at
   1000. A client asking for more is almost always a bug, not a need.
5. **When you truncate, say so.** Add a `truncated`/`next_cursor`
   field. Silent truncation looks like "no more data" to a user.
6. **For "all of X" listings (services, integrations, etc.), cap
   anyway.** An infinite list is rarely the right UX; a 5000-row cap
   is your seat belt against malformed data.

## Operational notes

- Every CH query uses a per-request `context.Context` already. The
  P0-3 settings give a server-side ceiling on top of that.
- The `clickhouse-go/v2` driver pools connections per endpoint with
  reasonable defaults; not tuned here.
- ClickHouse system tables (`system.query_log`, `system.parts`) are
  the right next inspection target if a particular query goes slow in
  production. Out of scope for this audit.
