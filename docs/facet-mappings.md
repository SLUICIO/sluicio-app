# Facet attribute mappings

The built-in service facets (file-input, queue-output, http-input, …)
classify a service based on the `io.kind` and `io.role` attributes its
spans carry. That works for services instrumented with those keys, but
many real services emit OpenTelemetry traces without them — yet still
clearly do file I/O, message-queue work, HTTP, and so on.

Facet attribute mappings let an operator say, in the UI, *"for this
service, treat spans where attribute X satisfies condition Y as
carrying io.kind=K and io.role=R."* The cell-api applies those rules
implicitly inside every classification and per-facet widget query, so
the service lands on the right facet dashboards without being
re-instrumented.

## Data model

A mapping lives in the `service_facet_mappings` table in Postgres:

```
service_facet_mappings
 ├─ id, organization_id, service_name      ← lookup key
 ├─ attribute_source   (span | resource)   ← where to read from
 ├─ attribute_key      TEXT                ← which key
 ├─ match_operator     (equals | prefix | suffix | contains | exists)
 ├─ match_value        TEXT                ← empty when operator='exists'
 ├─ set_io_kind        TEXT                ← file | queue | stream | http | db | email
 └─ set_io_role        TEXT                ← input | output
```

Keyed by `(organization_id, service_name)` for the same reason as
service_tags: services aren't first-class rows in Postgres, they're
discovered from ClickHouse. A rule survives the service going quiet
for a window and comes back when it does.

Regex is intentionally *not* in the v1 operator vocabulary; the
additional query-injection surface (regex literals interpolated into
ClickHouse SQL) isn't worth the modest UX gain. Customers can ask if
they need it.

## How rules are applied

`facetmappings.BuildResolver([]Mapping)` compiles a service's rules
into two SQL expressions:

```sql
-- effective io.kind for this service:
coalesce(
  nullIf(SpanAttributes['io.kind'], ''),
  CASE
    WHEN SpanAttributes['peer.service'] = ?      THEN ?
    WHEN SpanAttributes['messaging.system'] != ' '  THEN ?
    ELSE ''
  END
)
```

(plus the equivalent expression for `io.role`). The raw span attribute
takes precedence over any rule — if the service starts emitting
`io.kind` correctly later, manual rules become a no-op for those
spans without anyone having to clean up.

At request time:

- The handler builds a `Resolver` for the focal service via
  `Handlers.ioResolverFor`, which loads the rules with a single
  indexed Postgres query.
- `store.ServiceProfile` and every `Widget.Compute` accept the
  `Resolver` and interpolate `KindExpr` / `RoleExpr` in place of the
  raw `SpanAttributes['io.kind']` / `['io.role']` lookups.
- `SpanFilter.SQL` recognises `AttrEquals` entries on `io.kind` /
  `io.role` and uses the resolver expressions there too, so every
  built-in facet widget (file-input throughput, http-output latency,
  etc.) automatically respects user-defined rules without per-widget
  changes.

The resolver is built once per request and threaded through every
widget — no per-widget configuration, no globals, no caching across
requests. The cost is one tightly-indexed Postgres SELECT per service
detail page load.

## API

```
GET    /api/v1/services/{name}/facet-mappings
POST   /api/v1/services/{name}/facet-mappings
DELETE /api/v1/services/{name}/facet-mappings/{id}
```

POST body:

```json
{
  "attribute_source": "span",
  "attribute_key":    "peer.service",
  "match_operator":   "equals",
  "match_value":      "sftp.bank.com",
  "set_io_kind":      "file",
  "set_io_role":      "input"
}
```

The handler normalises `attribute_source`, `match_operator`,
`set_io_kind`, and `set_io_role` to lowercase + trims whitespace
before validation. `Mapping.Validate` enforces the closed sets for
the enum-like fields and rejects empty values for non-`exists`
operators.

## UX

A minimal CRUD editor on the service detail page (`FacetMappingsEditor`)
lists every rule for the service and exposes an inline add form. No
preview pane in v1 — the next iteration should show which spans in
the current window would match a draft rule before the user commits,
plus a detection-assist that suggests likely (kind, role) pairs from
the service's attribute profile when no facets have matched
automatically.

## Edge cases

- **Exists operator stores empty value.** The store layer forces
  `match_value = ""` when `match_operator = 'exists'` so the DB
  CHECK constraint (`match_operator = 'exists' OR match_value <> ''`)
  is never violated by a buggy client.
- **Raw attribute wins.** A rule never overrides an existing
  `io.kind` / `io.role` on a span. This keeps the rules a "fallback"
  story — adding instrumentation later automatically supersedes
  the rule without coordination.
- **Unknown operator fails closed.** The SQL builder emits `1=0`
  for an unrecognised operator so a future enum drift never
  silently misclassifies spans.
- **Identity resolver.** `facetmappings.IdentityResolver()` returns
  the raw lookups with empty arg slices, so callers can use the
  resolver unconditionally — no branching at the SQL site.

## Follow-ups

- **Detection-assist suggestions.** When `MatchAll(profile)` returns
  only `core` (no I/O facets fired), the UI could suggest plausible
  rules from the observed attributes — e.g. "we see `http.route` on
  80 % of spans, want to classify as http-input?"
- **Preview before commit.** Before the user saves a rule, run a
  quick count of spans matching the predicate in the current
  window and render "this would tag N of M spans."
- **Resource-attribute breakdowns.** Existing breakdown widgets use
  span attributes; mappings can already target resource attributes
  for the condition, but the breakdowns still pull from
  `SpanAttributes`. Consider adding `ResourceAttributes` as an
  intrinsic source.
- **Regex operator.** Parameterised regex through ClickHouse `match()`
  would be safe; defer until we see a clear customer need.
- **Effective profile export.** A "show me what `io_facet` values this
  service derives" endpoint would help users debug their rules.
