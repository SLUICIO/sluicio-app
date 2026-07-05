// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationMessages — the Messages tab on an integration detail
// page. This is a scoped variant of the global Message Search; the
// FilterEditor is the same component, but the "integration" filter is
// pre-set and locked to the route's integration. Any filter combo can
// be saved as a view that surfaces both here and on the global search
// page (badged "in <integration>").
//
// Per the Sluicio handoff:
//   - locked pill rendering + interaction rules
//   - plain-English summary leads with the locked scope
//   - save-as-view callout visible when the user has added any free
//     filter to the locked scope, or no view is loaded
//   - URL state (?q / ?t / ?s) is the canonical source for the filter
//     set so links are shareable
//   - the locked filter is always added on top regardless of URL
//   - scope banner above the editor (role + accessible-integration
//     count + redacted-fields note)

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import FilterEditor, { type Field, type Filter } from "../components/search/FilterEditor";
import SaveAsViewDialog from "../components/search/SaveAsViewDialog";
import SharePermalinkButton from "../components/search/SharePermalinkButton";
import IntegrationTabs from "../components/IntegrationTabs";
import { integrationProblemCount } from "../lib/integrationHealth";
import IntegrationPageHeader from "../components/IntegrationPageHeader";
import { StatusPip } from "../components/primitives";
import VirtualInfiniteList from "../components/VirtualInfiniteList";
import TraceDrawer from "../components/TraceDrawer";
import type {
  Integration,
  IntegrationDetail,
  MessageAttributeKey,
  MessageCursor,
  MessageFilter,
  MessageView,
  TraceSearchResult,
} from "../api/types";
import type { SavedView } from "../components/search/types";
import { formatDateTime, formatDurationMs, formatRelative } from "../lib/format";
import {
  hydrateFiltersFromUrl,
  writeFiltersToParams,
} from "../lib/messageFilterUrl";
import { useCurrentUser } from "../lib/useCurrentUser";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";
import {
  CSV_EXPORT_CAP,
  csvFilename,
  downloadCsv,
  fetchAllMessages,
  messageRowsToCsv,
} from "../lib/messagesCsv";

// The add-field options offered in the filter editor on this page.
// "integration" is intentionally omitted — the page is already scoped to one
// integration (the locked row), so filtering by integration here is
// redundant. Everything else stays available.
const INTEGRATION_FILTER_FIELDS: Field[] = ["payload", "status", "service", "errorType", "traceId", "spanId"];

// makeLockedIntegrationFilter builds the locked row for this page.
// integrationName is what the search engine matches against, so we
// use the human-readable name rather than the UUID id — same as the
// global integration value picker.
function makeLockedIntegrationFilter(integrationName: string): Filter {
  return {
    id: "scope-integration",
    field: "integration",
    op: "is",
    value: integrationName,
    removable: false,
    locked: true,
  };
}

// filtersToWire mirrors the converter in Search.tsx so both pages
// serialize the same shape. Time filters are dropped — the time range
// comes from the page-wide header selector, not an in-search row.
function filtersToWire(filters: Filter[]): MessageFilter[] {
  return filters
    .filter((f) => !f.optional && f.field !== "time")
    .map((f) => ({
      field: f.field,
      fieldPath: f.fieldPath,
      op: f.op,
      value: f.value,
      removable: f.removable,
      locked: f.locked,
    }));
}

function viewFromWire(v: MessageView): SavedView {
  return {
    id: v.id,
    name: v.name,
    mine: v.mine,
    pinned: v.pinned,
    sharedWith: v.shared ? ["team"] : undefined,
    resultCount: v.resultCount,
    lastEditedAt: v.lastEditedAt,
    scope: v.scope?.integrationId
      ? { integrationId: v.scope.integrationId, serviceId: v.scope.serviceId }
      : undefined,
    // Drop any legacy time filter — time is the header's job now.
    filters: v.filters
      .filter((f) => f.field !== "time")
      .map((f) => ({
        id: f.id ?? crypto.randomUUID(),
        field: f.field,
        fieldPath: f.fieldPath,
        op: f.op,
        value: f.value,
        removable: f.removable ?? true,
        locked: f.locked,
        optional: f.optional,
      })),
  };
}

export default function IntegrationMessagesPage() {
  const { id = "" } = useParams();
  const [windowVal] = useTimeWindow();
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");

  // Delayed-only mode: arrived via the Overview "delayed (open)" tile
  // (`?delayed=1`). When on, the trace list is filtered to traces with an
  // open trace-completion firing — the dedicated view of SLA breaches that
  // used to live as a table on the Settings tab.
  const [delayedOnly, setDelayedOnly] = useState(
    () => new URLSearchParams(window.location.search).get("delayed") === "1",
  );
  const clearDelayedOnly = () => {
    setDelayedOnly(false);
    const params = new URLSearchParams(window.location.search);
    params.delete("delayed");
    const next = params.toString();
    window.history.replaceState(
      null,
      "",
      `${window.location.pathname}${next ? `?${next}` : ""}`,
    );
  };

  const [detail, setDetail] = useState<IntegrationDetail | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);

  // The locked filter can't be built until we know the integration's
  // human-readable name (the value the search engine matches against).
  // We hold the user-set filters separately and prepend the locked one
  // whenever we hand the list to FilterEditor or the search call.
  const [userFilters, setUserFilters] = useState<Filter[]>(() =>
    hydrateFiltersFromUrl(window.location.search),
  );

  const [allViews, setAllViews] = useState<SavedView[]>([]);
  const [activeView, setActiveView] = useState<SavedView | null>(null);
  const [showOpenSaved, setShowOpenSaved] = useState(false);
  const [showSaveDialog, setShowSaveDialog] = useState(false);

  const [results, setResults] = useState<TraceSearchResult[]>([]);
  const [hasResults, setHasResults] = useState(false);
  const [nextCursor, setNextCursor] = useState<MessageCursor | undefined>(undefined);
  const [hasMore, setHasMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const loadingRef = useRef(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [streamLive, setStreamLive] = useState(false);
  const [exporting, setExporting] = useState(false);
  // The trace whose waterfall + attributes are shown in the drawer.
  const [openTraceId, setOpenTraceId] = useState<string | null>(null);

  const PAGE = 100;
  const messagesListHeight = useMemo(
    () => (typeof window !== "undefined" ? Math.max(320, window.innerHeight - 470) : 480),
    [],
  );

  // Org integrations are used by the FilterEditor's integration value
  // picker (for any *additional* integration row the user adds — they
  // shouldn't usually, but the editor's picker still expects this).
  const [integrations, setIntegrations] = useState<Integration[]>([]);
  useEffect(() => {
    api
      .listIntegrations(windowVal)
      .then((r) => setIntegrations(r.integrations ?? []))
      .catch(() => setIntegrations([]));
  }, [windowVal]);

  // Payload attribute keys scoped to *this* integration's traffic — drive
  // the field typeahead so the user picks from attributes that actually flow
  // through the integration rather than typing blind.
  const [attrKeys, setAttrKeys] = useState<MessageAttributeKey[]>([]);
  useEffect(() => {
    if (!id) return;
    api
      .integrationAttributeKeys(id, windowVal)
      .then((r) => setAttrKeys(r.attribute_keys ?? []))
      .catch(() => setAttrKeys([]));
  }, [id, windowVal]);

  // Value typeahead source: top-N observed values for an attribute key, scoped
  // to this integration + window. Fetched lazily when a value pill opens.
  const fetchAttrValues = useCallback(
    (key: string) =>
      api
        .integrationAttributeValues(id, key, windowVal)
        .then((r) => (r.values ?? []).map((v) => ({ value: v.value, count: v.events }))),
    [id, windowVal],
  );

  useEffect(() => {
    if (!id) return;
    setDetailError(null);
    api
      .getIntegration(id, windowVal)
      .then(setDetail)
      .catch((e) => setDetailError(String(e.message ?? e)));
  }, [id, windowVal]);

  // Trace-completion firings — used to mark delayed rows in the
  // trace list below. We don't need to block the page on this; a
  // missing fetch just means no "delayed" badge until the next
  // refresh cycle.
  //
  // Indexed by trace_id → severity of the matching firing. This
  // drives both the row's StatusPip kind (warn / err) AND the
  // "delayed" badge colour: a 'warning' rule shows yellow, a
  // 'critical' rule shows red — treating SLA breaches as
  // first-class status signals on the trace itself, not just a
  // side badge.
  //
  // Re-polled every 30s (matches the evaluator cadence) so a
  // trace that flips to delayed after the page is open shows up
  // without a manual refresh. A trace can in principle match
  // multiple rules; we keep the highest severity (critical > warning > info).
  const [delayedSeverity, setDelayedSeverity] = useState<
    Map<string, "info" | "warning" | "critical">
  >(new Map());
  // Traces that had a firing that has since resolved — the closing
  // span eventually arrived, but past the SLA. We show these with
  // a softer "delivered with delay" badge so the audit trail stays
  // visible without overstating the current status.
  const [deliveredWithDelay, setDeliveredWithDelay] = useState<Set<string>>(
    new Set(),
  );
  // trace_id → the open firing instance ids for that trace. A trace can
  // have several open stage-firings; "mark handled" acks them all so the
  // Overview "delayed (open)" count (which counts instances) clears for
  // the whole trace. Drives the mark-handled action in delayed-only mode.
  const [delayedInstances, setDelayedInstances] = useState<
    Map<string, string[]>
  >(new Map());
  const reloadFirings = useCallback(() => {
    if (!id) return;
    const sevRank = (s: string) =>
      s === "critical" ? 3 : s === "warning" ? 2 : s === "info" ? 1 : 0;
    // Scope the firing set to the header window so the delayed badges and
    // the delayed-only filter line up with the traces actually shown.
    return api
      .listCompletionFirings(id, windowVal)
      .then((r) => {
        // Build the views in one pass: active severity for any
        // currently-firing row, the open instance ids per trace, and a
        // separate set of trace_ids with at least one RESOLVED firing.
        const active = new Map<string, "info" | "warning" | "critical">();
        const instances = new Map<string, string[]>();
        const resolved = new Set<string>();
        for (const f of r.firings) {
          // A handled firing is benign — it gets neither the delayed
          // badge nor the delivered-with-delay badge.
          if (f.handled_at) continue;
          if (f.state === "firing") {
            const prev = active.get(f.trace_id);
            if (!prev || sevRank(f.severity) > sevRank(prev)) {
              active.set(f.trace_id, f.severity);
            }
            const ids = instances.get(f.trace_id) ?? [];
            ids.push(f.instance_id);
            instances.set(f.trace_id, ids);
          } else if (f.state === "resolved") {
            resolved.add(f.trace_id);
          }
        }
        setDelayedSeverity(active);
        setDelayedInstances(instances);
        setDeliveredWithDelay(resolved);
      })
      .catch(() => {
        /* network blip — silent; next tick retries */
      });
  }, [id, windowVal]);
  useEffect(() => {
    if (!id) return;
    reloadFirings();
    const t = window.setInterval(reloadFirings, 30000);
    return () => window.clearInterval(t);
  }, [id, reloadFirings]);

  // Mark every open firing on a trace handled, then re-poll so the badge
  // and the Overview count update without waiting for the 30s tick.
  const markTraceHandled = useCallback(
    async (traceId: string) => {
      const ids = delayedInstances.get(traceId) ?? [];
      if (ids.length === 0) return;
      try {
        await Promise.all(
          ids.map((iid) => api.markCompletionFiringHandled(id, iid)),
        );
        await reloadFirings();
      } catch (e) {
        alert(String((e as Error).message ?? e));
      }
    },
    [delayedInstances, id, reloadFirings],
  );

  const integrationName = detail?.integration.name ?? "";

  usePageTitle(
    detail?.integration.name
      ? `Messages · ${detail.integration.name}`
      : "Messages",
  );

  const lockedFilter = useMemo<Filter | null>(
    () => (integrationName ? makeLockedIntegrationFilter(integrationName) : null),
    [integrationName],
  );

  // The composed filter list handed to FilterEditor / search. The
  // locked filter is always added on top regardless of URL state.
  const composedFilters: Filter[] = useMemo(
    () => (lockedFilter ? [lockedFilter, ...userFilters] : userFilters),
    [lockedFilter, userFilters],
  );

  // Load saved views. We surface both views scoped to this integration
  // and unscoped/global ones, with a per-section header in the menu.
  useEffect(() => {
    api
      .listMessageViews()
      .then((r) => setAllViews((r.views ?? []).map(viewFromWire)))
      .catch(() => setAllViews([]));
  }, [id]);

  const scopedViews = useMemo(
    () => allViews.filter((v) => v.scope?.integrationId === id),
    [allViews, id],
  );
  const globalViews = useMemo(
    () => allViews.filter((v) => !v.scope?.integrationId),
    [allViews],
  );

  // Sync URL whenever the user changes filters. We only encode
  // user-set filters — the locked row is implied by the route.
  useEffect(() => {
    // Preserve other query params — notably ?range= set by the header
    // time selector. We only own ?s / ?q here; ?t is legacy and dropped.
    const params = new URLSearchParams(window.location.search);
    writeFiltersToParams(userFilters, params);
    const next = params.toString();
    const target = `${window.location.pathname}${next ? `?${next}` : ""}`;
    if (target !== `${window.location.pathname}${window.location.search}`) {
      window.history.replaceState(null, "", target);
    }
  }, [userFilters]);

  // Run the search whenever composedFilters changes — but only once
  // we have a locked filter (i.e. the integration name is known).
  const run = useCallback(() => {
    if (!lockedFilter) return;
    setLoading(true);
    setError(null);
    api
      .searchMessages({
        range: windowVal,
        filters: filtersToWire(composedFilters),
        limit: PAGE,
      })
      .then((r) => {
        setResults(r.results ?? []);
        setNextCursor(r.next_cursor);
        setHasMore(!!r.next_cursor);
        setHasResults(true);
      })
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  }, [composedFilters, lockedFilter, windowVal]);

  useEffect(() => {
    const t = window.setTimeout(run, 350);
    return () => window.clearTimeout(t);
  }, [run]);

  const loadMore = useCallback(() => {
    if (!nextCursor || loadingRef.current) return;
    loadingRef.current = true;
    setLoadingMore(true);
    api
      .searchMessages({
        range: windowVal,
        filters: filtersToWire(composedFilters),
        limit: PAGE,
        cursor: nextCursor,
      })
      .then((r) => {
        setResults((prev) => [...prev, ...(r.results ?? [])]);
        setNextCursor(r.next_cursor);
        setHasMore(!!r.next_cursor);
      })
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => {
        loadingRef.current = false;
        setLoadingMore(false);
      });
  }, [nextCursor, composedFilters, windowVal]);

  // Export the whole filtered result set to CSV. Paginates the same
  // /messages/search endpoint the table uses (up to CSV_EXPORT_CAP) so the
  // file isn't limited to the rows currently loaded into the table.
  const onExportCsv = useCallback(async () => {
    if (exporting) return;
    setExporting(true);
    setError(null);
    try {
      const { rows, capped } = await fetchAllMessages({
        range: windowVal,
        filters: filtersToWire(composedFilters),
      });
      if (rows.length === 0) {
        setError("Nothing to export for the current filters.");
        return;
      }
      downloadCsv(csvFilename(integrationName), messageRowsToCsv(rows));
      if (capped) {
        setError(
          `Export capped at ${CSV_EXPORT_CAP.toLocaleString()} rows — narrow the filters or time window for the rest.`,
        );
      }
    } catch (e) {
      setError(`Export failed: ${String((e as Error).message ?? e)}`);
    } finally {
      setExporting(false);
    }
  }, [exporting, windowVal, composedFilters, integrationName]);

  // Filter changes are dispatched from FilterEditor as a full list; we
  // strip the locked row (which the editor keeps but doesn't let the
  // user edit) and store the user-set rest.
  const onFiltersChange = (next: Filter[]) => {
    setUserFilters(next.filter((f) => !f.locked));
  };

  const isDirty = useMemo(() => {
    if (!activeView) return userFilters.length > 0;
    const viewUser = activeView.filters.filter((f) => !f.locked);
    return !filtersEqual(viewUser, userFilters);
  }, [activeView, userFilters]);

  // Loading a saved view: replace the user filters with the view's
  // non-locked filters. The page's own locked filter stays in place.
  const loadView = (v: SavedView) => {
    setActiveView(v);
    setUserFilters(
      v.filters
        .filter((f) => !f.locked && f.field !== "time")
        .map((f) => ({ ...f, id: f.id || crypto.randomUUID() })),
    );
    setShowOpenSaved(false);
  };

  const onSaveAsView = async (values: {
    name: string;
    description: string;
    visibility: "private" | "team" | "org";
    pinned: boolean;
  }) => {
    try {
      const created = await api.createMessageView({
        name: values.name,
        description: values.description || undefined,
        pinned: values.pinned,
        // visibility v1: private → shared=false, team/org → shared=true.
        // A future MessageView API can carry the full enum.
        shared: values.visibility !== "private",
        filters: filtersToWire(composedFilters),
        scope: { integrationId: id },
      });
      const v = viewFromWire(created);
      setAllViews((curr) => [v, ...curr]);
      setActiveView(v);
      setShowSaveDialog(false);
    } catch (e) {
      setError(`Save failed: ${String((e as Error).message ?? e)}`);
    }
  };

  // In delayed-only mode the list is filtered client-side to traces with
  // an open firing. The match set comes from the firings poll, so it's
  // exact for the loaded page; "load more" pulls more traces from the
  // search and surfaces any further-back delayed ones.
  const visibleResults = useMemo(
    () =>
      delayedOnly
        ? results.filter((r) => delayedSeverity.has(r.trace_id))
        : results,
    [delayedOnly, results, delayedSeverity],
  );

  // In delayed-only mode the filtered list can be too short to scroll,
  // so the on-scroll loadMore never fires and delayed traces past the
  // first page stay hidden. Auto-fetch more pages until every known
  // delayed trace (from the firings poll) is loaded or we run out of
  // pages. Bounded by delayedSeverity.size, which is small.
  useEffect(() => {
    if (!delayedOnly || !hasMore || loadingMore) return;
    if (visibleResults.length < delayedSeverity.size) loadMore();
  }, [
    delayedOnly,
    hasMore,
    loadingMore,
    visibleResults.length,
    delayedSeverity.size,
    loadMore,
  ]);

  const loadedLabel = `${visibleResults.length}${hasMore ? "+" : ""} result${
    visibleResults.length === 1 ? "" : "s"
  }`;

  // Tab badge: the integration-level distinct message count from the
  // detail response, so it matches the Overview tab (both count distinct
  // traces, not per-service-summed). The footer below still shows how
  // many rows are actually loaded for the current filter set.
  const messagesCount = detail?.message_count;

  return (
    <div className="flex flex-col gap-4">
      {/* Shared identity header — same breadcrumb + status ("Receiving
          data" etc.) as every other integration tab. The Messages
          actions live in the header's right-side actions slot. */}
      <IntegrationPageHeader
        detail={detail}
        actions={
          <>
            <OpenSavedMenu
              open={showOpenSaved}
              onToggle={() => setShowOpenSaved((o) => !o)}
              onClose={() => setShowOpenSaved(false)}
              scopedViews={scopedViews}
              globalViews={globalViews}
              onSelect={loadView}
            />
            <SharePermalinkButton
              path={`/integrations/${encodeURIComponent(id)}/messages`}
              filters={userFilters}
              range={windowVal}
            />
            <button
              type="button"
              className="btn btn--primary"
              onClick={() => setShowSaveDialog(true)}
              disabled={!lockedFilter}
              title={
                activeView
                  ? "Saves the current filter set as a new view"
                  : "Saves these filters as a view"
              }
            >
              💾 save as view
            </button>
          </>
        }
      />

      <IntegrationTabs integrationId={id} messagesCount={messagesCount} errorsCount={integrationProblemCount(detail)} />

      {detailError && (
        <div className="alert alert--error">{detailError}</div>
      )}

      {/* Delayed-only banner — shown when the user arrived from the
          Overview "delayed (open)" tile. The list below is filtered to
          traces with an open completion firing. */}
      {delayedOnly && (
        <div
          className="flex items-center gap-3 rounded-md border px-3 py-2 text-sm"
          style={{
            borderColor: "color-mix(in oklab, var(--warn) 40%, transparent)",
            background: "color-mix(in oklab, var(--warn) 12%, transparent)",
          }}
        >
          <span aria-hidden="true">⏳</span>
          <div className="flex-1">
            <b>Delayed traces only</b>
            <span className="ml-1 text-muted">
              — showing traces that breached a trace-completion SLA and are
              still open ({delayedSeverity.size} in this window). Manage the SLA
              rules on the{" "}
              <Link
                to={`/integrations/${encodeURIComponent(id)}/settings`}
                className="underline-offset-2 hover:underline"
              >
                Settings tab
              </Link>
              .
            </span>
          </div>
          <button
            type="button"
            className="font-medium underline-offset-2 hover:underline"
            onClick={clearDelayedOnly}
          >
            clear filter
          </button>
        </div>
      )}

      {/* If a saved view is loaded, surface its name + dirty state. */}
      {activeView && (
        <div className="flex items-center justify-between rounded-md border bg-surface-2 px-3 py-2 text-xs"
          style={{ borderColor: "var(--border)" }}
        >
          <div>
            <span className="uppercase tracking-wide text-muted">view · </span>
            <span className="font-semibold">{activeView.name}</span>
            {activeView.lastEditedAt && (
              <span className="ml-2 text-muted">
                edited {formatRelative(activeView.lastEditedAt)}
              </span>
            )}
            {isDirty && (
              <span className="ml-2 font-medium" style={{ color: "var(--warn)" }}>
                ● unsaved changes
              </span>
            )}
          </div>
          <div className="flex items-center gap-3">
            {isDirty && (
              <button
                type="button"
                className="hover:underline"
                onClick={() => loadView(activeView)}
              >
                ↶ reset to saved
              </button>
            )}
            <button
              type="button"
              className="hover:underline"
              onClick={() => {
                setActiveView(null);
                setUserFilters([]);
              }}
            >
              clear view
            </button>
          </div>
        </div>
      )}

      <FilterEditor
        filters={composedFilters}
        onChange={onFiltersChange}
        knownIntegrations={integrations.map((i) => ({ id: i.id, name: i.name }))}
        fields={INTEGRATION_FILTER_FIELDS}
        attributeKeys={attrKeys}
        fetchAttrValues={fetchAttrValues}
      />

      {/* Save-as-view callout — shown when the user has added at least
          one non-locked filter or no view is loaded. Mirrors the
          handoff's "nudge" pattern. The action is also always
          available via the top-right button. */}
      {(!activeView || isDirty) && userFilters.length > 0 && (
        <div
          className="flex items-center gap-3 rounded-md border px-3 py-2 text-sm"
          style={{
            borderColor: "color-mix(in oklab, var(--primary) 30%, transparent)",
            background: "var(--primary-soft)",
            color: "var(--primary-ink)",
          }}
        >
          <span aria-hidden="true">💾</span>
          <div className="flex-1">
            <b>Save these filters as a view</b>
            <span className="ml-1 text-muted">
              — available on this integration's Messages tab and in the global
              search rail (scope: {integrationName}).
            </span>
          </div>
          <button
            type="button"
            className="font-medium underline-offset-2 hover:underline"
            onClick={() => setShowSaveDialog(true)}
          >
            + save as view
          </button>
        </div>
      )}

      {/* Results */}
      <section
        className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border bg-surface-2"
        style={{ borderColor: "var(--border)" }}
      >
        {error && <div className="alert alert--error m-3">{error}</div>}
        {loading && results.length === 0 && (
          <div className="px-4 py-3 text-sm text-muted">Searching…</div>
        )}

        <div className="min-h-0 flex-1">
          <VirtualInfiniteList<TraceSearchResult>
            items={visibleResults}
            hasMore={hasMore}
            loadingMore={loadingMore}
            loadMore={loadMore}
            gridTemplate="24px 110px 160px 1fr 1fr 80px 56px"
            rowHeight={40}
            height={messagesListHeight}
            itemKey={(r) => r.trace_id}
            onRowClick={(r) => {
              if (r.trace_id) setOpenTraceId(r.trace_id);
            }}
            rowClassName={(r) => (r.trace_id ? "cursor-pointer hover:bg-surface-3" : "cursor-not-allowed opacity-60")}
            empty={
              <div className="px-4 py-6 text-sm text-muted">
                {delayedOnly
                  ? "No delayed traces in this window. When a trace breaches a completion SLA it'll appear here."
                  : hasResults
                    ? "No messages match in this integration for the current filters."
                    : "Set up filters above to run a search."}
              </div>
            }
            header={
              <>
                <span></span>
                <span>time</span>
                <span>msg id</span>
                <span>service · matched</span>
                <span>matched fields</span>
                <span className="text-right">duration</span>
                <span></span>
              </>
            }
            renderRow={(r) => {
              const sev = delayedSeverity.get(r.trace_id);
              const wasDelayed = !sev && deliveredWithDelay.has(r.trace_id);
              // Combined status: a real error span ALWAYS wins
              // (errors are unambiguous); otherwise a critical-
              // severity firing escalates to err, warning stays
              // warn, info falls back to ok-styled (info is more
              // informational than a status downgrade).
              // "delivered with delay" stays ok-pip — the trace
              // is fine NOW, the badge tells you it wasn't.
              const pipKind: "err" | "warn" | "ok" = r.has_error
                ? "err"
                : sev === "critical"
                  ? "err"
                  : sev === "warning"
                    ? "warn"
                    : "ok";
              const badgeClass =
                sev === "critical"
                  ? "badge sev-critical"
                  : sev
                    ? "badge sev-warning"
                    : "badge sev-info";
              const badgeLabel = sev ? "delayed" : "delivered with delay";
              const badgeTitle = sev
                ? `This trace exceeded its completion SLA (${sev}) — see Settings → Trace completion rules`
                : "This trace eventually delivered but breached its completion SLA on the way — see Settings → Trace completion rules";
              const showBadge = Boolean(sev) || wasDelayed;
              return (
              <>
                <StatusPip kind={pipKind} />
                <span className="font-mono text-xs text-muted">
                  {formatDateTime(r.trace_start)}
                </span>
                {r.trace_id ? (
                  <span className="truncate font-mono text-xs" style={{ color: "var(--primary)" }}>
                    {r.trace_id.slice(0, 16)}…
                  </span>
                ) : (
                  <span
                    className="truncate font-mono text-xs text-muted"
                    title="This span was ingested without a trace ID, so the full trace can't be opened. Set trace_id / span_id on the producer's OpenTelemetry spans."
                  >
                    no trace ID — can't open
                  </span>
                )}
                <span className="truncate" style={{ display: "flex", alignItems: "center", gap: 6, minWidth: 0 }}>
                  <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>
                    {r.matched_service}
                    <span className="text-muted"> · {r.matched_span_name}</span>
                  </span>
                  {showBadge && (
                    <span
                      className={badgeClass}
                      style={{ flexShrink: 0, fontSize: 10, padding: "1px 6px" }}
                      title={badgeTitle}
                    >
                      {badgeLabel}
                    </span>
                  )}
                </span>
                <span className="truncate font-mono text-xs text-muted">
                  {r.attributes &&
                    Object.entries(r.attributes)
                      .slice(0, 3)
                      .map(([k, v]) => `${k}=${v}`)
                      .join(" · ")}
                </span>
                <span className="text-right font-mono text-xs text-muted">
                  {formatDurationMs(r.duration_ms)}
                </span>
                {delayedOnly && sev && canWrite ? (
                  // In delayed-only mode the trailing cell becomes the
                  // "mark handled" action (acks all open firings on the
                  // trace) — replacing the firings table that used to
                  // live on the Settings tab. stopPropagation keeps the
                  // row click (open drawer) from also firing.
                  <button
                    type="button"
                    className="text-right text-xs hover:underline"
                    style={{ color: "var(--warn)" }}
                    title="Mark this delayed trace as handled (e.g. the message was resent). It stops counting as delayed."
                    onClick={(e) => {
                      e.stopPropagation();
                      void markTraceHandled(r.trace_id);
                    }}
                  >
                    handle
                  </button>
                ) : (
                  <span
                    className="text-right text-xs"
                    style={{ color: "var(--primary)" }}
                  >
                    open ›
                  </span>
                )}
              </>
              );
            }}
          />
        </div>

        <footer
          className="flex items-center justify-between border-t px-4 py-2 text-xs text-muted"
          style={{ borderColor: "var(--border)" }}
        >
          <span>
            {hasResults
              ? `${loadedLabel} loaded · scope: ${integrationName} only`
              : "Set up filters above to run a search."}
          </span>
          <div className="flex items-center gap-3">
            <button
              type="button"
              className="hover:underline disabled:opacity-50"
              disabled={!hasResults || exporting}
              title={hasResults ? "Download the filtered results as CSV" : "Run a search first"}
              onClick={onExportCsv}
            >
              {exporting ? "exporting…" : "export CSV"}
            </button>
            <label className="inline-flex cursor-pointer items-center gap-1.5">
              <input
                type="checkbox"
                checked={streamLive}
                onChange={(e) => setStreamLive(e.target.checked)}
              />
              stream live
            </label>
          </div>
        </footer>
      </section>

      <SaveAsViewDialog
        open={showSaveDialog}
        filters={composedFilters}
        scope={{ integrationId: id, integrationName }}
        onClose={() => setShowSaveDialog(false)}
        onSubmit={onSaveAsView}
      />

      <TraceDrawer
        traceId={openTraceId}
        onClose={() => setOpenTraceId(null)}
        integrationContextId={id}
      />
    </div>
  );
}

// OpenSavedMenu — the "open saved ▾" dropdown in the page header.
// Shows scoped views first (marked "· here") then global ones.
interface OpenSavedMenuProps {
  open: boolean;
  onToggle: () => void;
  onClose: () => void;
  scopedViews: SavedView[];
  globalViews: SavedView[];
  onSelect: (v: SavedView) => void;
}

function OpenSavedMenu({
  open,
  onToggle,
  onClose,
  scopedViews,
  globalViews,
  onSelect,
}: OpenSavedMenuProps) {
  // Close on Escape or outside click.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  return (
    <div className="relative">
      <button
        type="button"
        className="btn"
        onClick={onToggle}
        aria-haspopup="menu"
        aria-expanded={open}
      >
        open saved ▾
      </button>
      {open && (
        <div
          role="menu"
          className="absolute right-0 top-full z-30 mt-1 w-72 rounded-lg border bg-surface-2 p-2 text-sm shadow-lg"
          style={{ borderColor: "var(--border)" }}
        >
          {scopedViews.length === 0 && globalViews.length === 0 && (
            <div className="px-2 py-3 text-muted">No saved views yet.</div>
          )}

          {scopedViews.length > 0 && (
            <>
              <div className="px-2 py-1 text-[10px] uppercase tracking-wide text-muted">
                on this integration
              </div>
              {scopedViews.map((v) => (
                <button
                  key={v.id}
                  type="button"
                  className="block w-full rounded px-2 py-1.5 text-left hover:bg-surface-3"
                  onClick={() => onSelect(v)}
                  role="menuitem"
                >
                  <span className="font-medium">{v.name}</span>
                  <span className="ml-2 text-xs text-muted">· here</span>
                </button>
              ))}
            </>
          )}

          {globalViews.length > 0 && (
            <>
              <div className="mt-2 px-2 py-1 text-[10px] uppercase tracking-wide text-muted">
                global views
              </div>
              {globalViews.map((v) => (
                <button
                  key={v.id}
                  type="button"
                  className="block w-full rounded px-2 py-1.5 text-left hover:bg-surface-3"
                  onClick={() => onSelect(v)}
                  role="menuitem"
                >
                  <span className="font-medium">{v.name}</span>
                </button>
              ))}
            </>
          )}
        </div>
      )}
    </div>
  );
}

function filtersEqual(a: Filter[], b: Filter[]): boolean {
  if (a.length !== b.length) return false;
  for (let i = 0; i < a.length; i++) {
    const x = a[i];
    const y = b[i];
    if (
      x.field !== y.field ||
      x.fieldPath !== y.fieldPath ||
      x.op !== y.op ||
      x.value !== y.value
    ) {
      return false;
    }
  }
  return true;
}

