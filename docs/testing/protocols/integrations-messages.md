<!-- SPDX-License-Identifier: FSL-1.1-Apache-2.0 -->

# Protocol: Integrations & messages

| Field | Value |
|-------|-------|
| **Area** | Integration lifecycle, matcher routing, messages, schemas, maps |
| **Automation status** | Partial (routes render in smoke; CRUD + routing assertions manual until seeded e2e) |
| **Automated by** | `e2e/tests/smoke.spec.ts` (render) |
| **Last reviewed** | 2026-06-20 |

## Preconditions
- Stack up; **seed telemetry** for routing/message cases; signed in as editor/admin for mutations.

## Integration lifecycle

### Case 1 — List integrations
- **Endpoint:** `GET /api/v1/integrations` · **Actor:** member
- **Steps:** Open Integrations; filter by tags (AND), by name/metadata; pick columns; sort.
- **Expected:** Status (unhealthy/errors/ok/quiet), trace/error counts, service count, tags, metadata, updated. Filter state in URL (`?tags=`,`?filter=`,`?cols=`). Health aggregates member services + open errors.
- **Code:** `handlers_integrations.go:50` · **Automation:** yes.

### Case 2 — Create integration
- **Endpoint:** `POST /api/v1/integrations` · **Actor:** editor/admin
- **Steps:** New integration → name/slug/description → add service-matching rule(s) (operator equals/prefix/suffix/contains/regex) → optional attribute conditions (AND/OR) → live boolean preview → optional tags → submit.
- **Expected:** Created; matchers persisted; catalog reconciled immediately; redirect to detail. Dependency suggestions offered for equals-rules over known services.
- **Code:** `handlers_integrations.go:551` · **Automation:** Partial (needs trace vocabulary).

### Case 3 — Edit integration (name/description)
- **Endpoint:** `PUT /api/v1/integrations/{id}` · **Expected:** Header + list card update; routing unaffected. · **Code:** `:756` · **Automation:** yes.

### Case 4 — Delete integration
- **Endpoint:** `DELETE /api/v1/integrations/{id}` · **Expected:** Removed from list; traces render without the association; dashboard tiles clear. · **Code:** `:788` · **Automation:** yes.

## Matcher routing (the core)

### Case 5 — Matcher rules route services/messages
- **Endpoints:** `POST/DELETE /api/v1/integrations/{id}/matchers`, convenience `DELETE …/services/{name}`
- **Steps:** Add/edit/remove service rules + attribute predicates with AND/OR; view live DNF preview; save.
- **Expected:** Rules expand to flat DNF on the wire; membership (`integration_services`) materializes immediately; resolver invalidated; per-service/attribute message counts recomputed. Most-specific-prefix wins. Removing an `equals` rule drops that service unless a prefix/regex rule still matches.
- **Code:** `handlers_integrations.go:809,844,871` · component [MatcherConfig.tsx](../../../frontend/src/components/MatcherConfig.tsx) · **Automation:** Partial. (Field-URL round-trip unit-tested in [messageFilterUrl.test.ts](../../../frontend/src/lib/messageFilterUrl.test.ts).)

### Case 6 — Span-name discovery for rule editors
- **Endpoint:** `GET /api/v1/integrations/{id}/span-names` · **Expected:** Start/stage pickers suggest distinct span names from the last 24h, by frequency. · **Code:** `:906` · **Automation:** yes.

## Messages

### Case 7 — Search messages (scoped + filtered)
- **Endpoint:** `POST /api/v1/messages/search`, fields via `GET /api/v1/messages/fields`
- **Steps:** Integration Messages tab (locked `integration=` row) → add payload-field / status / service filters (AND) → keyset-paginated trace list → open trace drawer. "Delayed only" filters to open completion firings.
- **Expected:** Restricted to matched services + attribute filters; window-scoped; filters serialize to `?q=`/`?s=` (shareable).
- **Code:** `handlers_messages.go:373` · **Automation:** Partial.

### Case 8 — Saved message views
- **Endpoints:** `GET/POST/PUT/DELETE /api/v1/message-views[/{id}]`
- **Steps:** Compose filters → Save as view (name, scope: integration±service) → appears in the picker on all message pages → load/edit/pin/share/delete.
- **Expected:** Filters hydrate, URL updates, result count shown; pinned views first.
- **Code:** `handlers_messages.go:93,113,147` · **Automation:** Partial.

### Case 9 — Export messages to CSV
- **Steps:** Apply filter → Export.
- **Expected:** Paginates all matches up to a 50k cap (warns when capped); CSV with trace_id/start/duration/has_error/spans/service_count/matched_service/matched_span/attributes; UTF-8 BOM; timestamped filename.
- **Code:** [messagesCsv.ts](../../../frontend/src/lib/messagesCsv.ts) · **Automation:** yes (API-driven).

### Case 10 — Share filter permalink
- **Expected:** Recipient opens URL → locked integration filter + `?q`/`?s` rehydrate → same result set. · **Code:** [messageFilterUrl.ts](../../../frontend/src/lib/messageFilterUrl.ts) · **Automation:** yes.

## Errors & delays

### Case 11 — Integration errors tab
- **Endpoint:** `GET /api/v1/errors` (also global Errors feed) · **Actor:** member
- **Expected:** Four buckets — unacknowledged (persisted) errors, failing health checks, failed traces, delayed traces — each with previews and drill-downs. Persisted errors are window-independent until acknowledged.
- **Code:** `handlers_errors.go:66` · **Automation:** Partial.

### Case 12 — Acknowledge errors / mark delayed trace handled
- **Endpoints:** `POST /api/v1/services/{name}/clear-errors`; `POST /api/v1/integrations/{id}/completion-firings/{iid}/handle`
- **Expected:** Ack bumps the watermark → effective error count zeros → row leaves the feed (returns on new errors). Marking a delayed trace handled flips it to "delivered with delay", drops it from open totals, keeps it for audit (sticky).
- **Code:** `handlers_trace_completion.go:339` · **Automation:** Partial.

## Schemas & maps

### Case 13 — Schemas CRUD + pin to service
- **Endpoints:** `GET/POST/PATCH/DELETE /api/v1/schemas[/{id}]`, `PUT /api/v1/services/{name}/schemas`
- **Steps:** Create schema (kind/format/version/content) → view (read-only) → edit → pin as a service's in/out schema → delete (shows in-use count; clears links).
- **Expected:** Pins drive flow-graph labels + map validation; delete cascades link removal.
- **Code:** `handlers_schemas.go:57,76,104,126` · **Automation:** Partial.

### Case 14 — Maps CRUD + execute
- **Endpoints:** `GET/POST/PATCH/DELETE /api/v1/maps[/{id}]`, `POST /api/v1/maps/{id}/execute`
- **Steps:** Create map (format xslt/jq/jsonata/liquid/…; body; optional from/to schema pins) → Test with sample input → save → execute.
- **Expected:** Transformation runs; if schemas pinned, output validated against the to-schema (inline status, non-fatal); missing engine surfaces an EngineError, not an HTTP 500.
- **Code:** `handlers_maps.go:50,69,97,115` · **Automation:** Partial (engine deps in CI).

## Notes
- Notification-profile assignment per integration is EE — see [alerts-notifications.md](alerts-notifications.md).
