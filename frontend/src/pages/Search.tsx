// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Message search — Saved view + friendly inline editor (variant A
// from the Sluicio handoff). Left rail of saved views; right column
// shows the active view's header (name + meta + dirty indicator),
// the role/scope banner, the sentence-style filter editor, and the
// results table.
//
// Saved views and the search query both go through the persisted
// cell-api endpoints:
//   GET    /api/v1/message-views
//   POST   /api/v1/message-views
//   PUT    /api/v1/message-views/{id}
//   DELETE /api/v1/message-views/{id}
//   POST   /api/v1/messages/search
//
// The page seeds an empty "untitled view" when the server returns no
// rows so the editor always has something to bind to.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api/client";
import TraceDrawer from "../components/TraceDrawer";
import FilterEditor, { type Filter } from "../components/search/FilterEditor";
import SaveAsViewDialog from "../components/search/SaveAsViewDialog";
import SavedViewsRail from "../components/search/SavedViewsRail";
import SharePermalinkButton from "../components/search/SharePermalinkButton";
import type { SavedView } from "../components/search/types";
import { StatusPip } from "../components/primitives";
import VirtualInfiniteList from "../components/VirtualInfiniteList";
import type {
  Integration,
  MessageCursor,
  MessageFieldDescriptor,
  MessageFilter,
  MessageView,
  TraceSearchResult,
} from "../api/types";
import { formatDateTime, formatDurationMs, formatRelative } from "../lib/format";
import { hydrateFiltersFromUrl } from "../lib/messageFilterUrl";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";
import {
  CSV_EXPORT_CAP,
  csvFilename,
  downloadCsv,
  fetchAllMessages,
  messageRowsToCsv,
} from "../lib/messagesCsv";

// ── view ↔ wire conversion ───────────────────────────────────────────
// SavedView (UI) <-> MessageView (wire). The shapes are nearly
// identical — Filter on the UI carries an "id" + "removable" used by
// the editor's per-row state, neither of which the server needs.
function viewFromWire(v: MessageView): SavedView {
  return {
    id: v.id,
    name: v.name,
    mine: v.mine,
    pinned: v.pinned,
    sharedWith: v.shared ? ["team"] : undefined,
    resultCount: v.resultCount,
    lastEditedAt: v.lastEditedAt,
    scope:
      v.scope?.integrationId || v.scope?.serviceId
        ? { integrationId: v.scope.integrationId, serviceId: v.scope.serviceId }
        : undefined,
    // Drop any legacy time filter — the time range is controlled by the
    // page-wide header selector, not an in-search row.
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

function filtersToWire(filters: Filter[]): MessageFilter[] {
  // Time is controlled by the page-wide header selector, not an
  // in-search row, so any stray time filter is dropped here.
  return filters
    .filter((f) => f.field !== "time")
    .map((f) => ({
      field: f.field,
      fieldPath: f.fieldPath,
      op: f.op,
      value: f.value,
      removable: f.removable,
      locked: f.locked,
      optional: f.optional,
    }));
}

// The empty-state placeholder shown when the user hits "new view"
// before saving. Lives only in memory until save fires.
function blankView(): SavedView {
  return {
    id: `draft-${crypto.randomUUID()}`,
    name: "untitled view",
    mine: true,
    pinned: false,
    filters: [],
  };
}

function isDraft(view: SavedView | undefined): boolean {
  return !!view && view.id.startsWith("draft-");
}

export default function Search() {
  usePageTitle("Messages");
  const [windowVal] = useTimeWindow();

  // Snapshot the URL once so a shared deep-link (?view=<id> or ?q/?s)
  // can be replayed after the saved views load, without re-reading a
  // location that we may later mutate.
  const initialParams = useRef(
    new URLSearchParams(window.location.search),
  ).current;

  const [views, setViews] = useState<SavedView[]>([]);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [draftFilters, setDraftFilters] = useState<Filter[]>([]);

  const [results, setResults] = useState<TraceSearchResult[]>([]);
  // The clicked message opens in a right-side trace blade (like the
  // service- and integration-scoped message views), not a full-page nav.
  const [openTraceId, setOpenTraceId] = useState<string | null>(null);
  const [hasResults, setHasResults] = useState(false);
  const [nextCursor, setNextCursor] = useState<MessageCursor | undefined>(undefined);
  const [hasMore, setHasMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const loadingRef = useRef(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [streamLive, setStreamLive] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [saving, setSaving] = useState(false);
  const [showSaveAsNew, setShowSaveAsNew] = useState(false);

  const [integrations, setIntegrations] = useState<Integration[]>([]);
  useEffect(() => {
    api
      .listIntegrations(windowVal)
      .then((r) => setIntegrations(r.integrations ?? []))
      .catch(() => setIntegrations([]));
  }, [windowVal]);

  // Field catalog for the current window: service names (filter by name,
  // not id) + every live attribute key (each becomes a filterable field).
  const [fieldCatalog, setFieldCatalog] = useState<MessageFieldDescriptor[]>([]);
  useEffect(() => {
    api
      .messageFields(windowVal)
      .then((r) => setFieldCatalog(r.fields ?? []))
      .catch(() => setFieldCatalog([]));
  }, [windowVal]);

  // Load saved views from the cell-api. If the org has none yet, drop
  // a single draft on the rail so the editor isn't pointed at nothing.
  useEffect(() => {
    let cancelled = false;
    api
      .listMessageViews()
      .then((r) => {
        if (cancelled) return;
        const mapped = (r.views ?? []).map(viewFromWire);

        // Replay a shared deep-link, in priority order:
        //   1. ?view=<id> → open that saved view (if it still exists)
        //   2. ?q / ?s    → open a draft seeded with the URL's filters
        //   3. otherwise  → the first saved view, or a fresh draft
        const wantView = initialParams.get("view");
        const target = wantView
          ? mapped.find((v) => v.id === wantView)
          : undefined;
        const urlFilters = hydrateFiltersFromUrl(initialParams.toString());

        if (target) {
          setViews(mapped.length > 0 ? mapped : [target]);
          setActiveId(target.id);
          setDraftFilters(target.filters);
        } else if (urlFilters.length > 0) {
          const v = blankView();
          setViews([...mapped, v]);
          setActiveId(v.id);
          setDraftFilters(urlFilters);
        } else if (mapped.length === 0) {
          const v = blankView();
          setViews([v]);
          setActiveId(v.id);
          setDraftFilters(v.filters);
        } else {
          setViews(mapped);
          setActiveId(mapped[0].id);
          setDraftFilters(mapped[0].filters);
        }
      })
      .catch((e) => {
        // Network / 500 — fall back to a draft so the page is usable.
        const v = blankView();
        setViews([v]);
        setActiveId(v.id);
        setDraftFilters(v.filters);
        setError(`Could not load saved views: ${String(e.message ?? e)}`);
      });
    return () => {
      cancelled = true;
    };
  }, [initialParams]);

  const activeView = views.find((v) => v.id === activeId) ?? null;

  const isDirty = useMemo(
    () => !!activeView && !filtersEqual(draftFilters, activeView.filters),
    [draftFilters, activeView],
  );

  const PAGE = 100;

  const run = useCallback(() => {
    setLoading(true);
    setError(null);
    api
      .searchMessages({
        range: windowVal,
        filters: filtersToWire(draftFilters),
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
  }, [draftFilters, windowVal]);

  // Auto-run when the filter set changes; debounced so a flurry of
  // pill edits doesn't hammer the API.
  useEffect(() => {
    const t = window.setTimeout(run, 350);
    return () => window.clearTimeout(t);
  }, [run]);

  // loadMore appends the next keyset page; the ref guard stops the
  // virtualized list firing it twice before state settles.
  const loadMore = useCallback(() => {
    if (!nextCursor || loadingRef.current) return;
    loadingRef.current = true;
    setLoadingMore(true);
    api
      .searchMessages({
        range: windowVal,
        filters: filtersToWire(draftFilters),
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
  }, [nextCursor, draftFilters, windowVal]);

  // Export the whole filtered result set to CSV — paginates the same
  // /messages/search endpoint (up to CSV_EXPORT_CAP), not just loaded rows.
  const onExportCsv = useCallback(async () => {
    if (exporting) return;
    setExporting(true);
    setError(null);
    try {
      const { rows, capped } = await fetchAllMessages({
        range: windowVal,
        filters: filtersToWire(draftFilters),
      });
      if (rows.length === 0) {
        setError("Nothing to export for the current filters.");
        return;
      }
      downloadCsv(csvFilename("search"), messageRowsToCsv(rows));
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
  }, [exporting, windowVal, draftFilters]);

  const selectView = (id: string) => {
    const v = views.find((v) => v.id === id);
    if (!v) return;
    // Opening a view here applies its filters in place — including
    // integration- or service-scoped views, which used to redirect to
    // their entity's tab. The scope just becomes a normal filter on the
    // Messages page, so we unlock it (off its home page there's no fixed
    // scope) and the user can edit or remove it like any other row.
    setActiveId(id);
    setDraftFilters(
      v.filters.map((f) =>
        f.locked ? { ...f, locked: false, removable: true } : f,
      ),
    );
  };

  const onCreateView = () => {
    const v = blankView();
    setViews([...views, v]);
    setActiveId(v.id);
    setDraftFilters(v.filters);
  };

  const onReset = () => {
    if (activeView) setDraftFilters(activeView.filters);
  };

  // Delete a saved view. Drafts live only in memory (drop locally);
  // persisted views are removed server-side first. If the deleted view
  // was active, fall back to the first remaining view, or a fresh draft.
  const onDeleteView = async (id: string) => {
    const v = views.find((x) => x.id === id);
    if (!v) return;
    const apply = () => {
      const remaining = views.filter((x) => x.id !== id);
      if (remaining.length === 0) {
        const b = blankView();
        setViews([b]);
        setActiveId(b.id);
        setDraftFilters(b.filters);
        return;
      }
      setViews(remaining);
      if (activeId === id) {
        setActiveId(remaining[0].id);
        setDraftFilters(remaining[0].filters);
      }
    };
    if (isDraft(v)) {
      apply();
      return;
    }
    try {
      await api.deleteMessageView(id);
      apply();
    } catch (e) {
      setError(`Delete failed: ${String((e as Error).message ?? e)}`);
    }
  };

  // Save:
  //  - For a draft (id starts with "draft-"), POST to create — the
  //    server-issued id then replaces the local placeholder.
  //  - For a real id, PUT to update.
  const onSave = async () => {
    if (!activeView) return;
    setSaving(true);
    try {
      if (isDraft(activeView)) {
        const created = await api.createMessageView({
          name: activeView.name,
          pinned: activeView.pinned,
          shared: activeView.sharedWith ? true : false,
          filters: filtersToWire(draftFilters),
        });
        const v = viewFromWire(created);
        setViews(views.map((x) => (x.id === activeView.id ? v : x)));
        setActiveId(v.id);
      } else {
        const updated = await api.updateMessageView(activeView.id, {
          name: activeView.name,
          pinned: activeView.pinned,
          shared: activeView.sharedWith ? true : false,
          filters: filtersToWire(draftFilters),
        });
        const v = viewFromWire(updated);
        setViews(views.map((x) => (x.id === v.id ? v : x)));
      }
    } catch (e) {
      setError(`Save failed: ${String((e as Error).message ?? e)}`);
    } finally {
      setSaving(false);
    }
  };

  const onSaveAsNewSubmit = async (values: {
    name: string;
    description: string;
    visibility: "private" | "team" | "org";
    pinned: boolean;
  }) => {
    setSaving(true);
    try {
      const created = await api.createMessageView({
        name: values.name,
        description: values.description || undefined,
        pinned: values.pinned,
        shared: values.visibility !== "private",
        filters: filtersToWire(draftFilters),
        // Global Search page → unscoped view. Scoped views are
        // created from the IntegrationMessages page where the locked
        // filter establishes the scope.
      });
      const v = viewFromWire(created);
      setViews([...views, v]);
      setActiveId(v.id);
      setShowSaveAsNew(false);
    } catch (e) {
      setError(`Save failed: ${String((e as Error).message ?? e)}`);
    } finally {
      setSaving(false);
    }
  };

  const onRename = async () => {
    if (!activeView) return;
    const name = window.prompt("Rename view", activeView.name);
    if (!name) return;
    // Optimistic local update; persist on next save. For an
    // already-persisted view, PUT immediately so the rail sticks
    // after a reload.
    const renamed: SavedView = { ...activeView, name };
    setViews(views.map((v) => (v.id === activeView.id ? renamed : v)));
    if (!isDraft(activeView)) {
      try {
        await api.updateMessageView(activeView.id, {
          name,
          pinned: activeView.pinned,
          shared: activeView.sharedWith ? true : false,
          filters: filtersToWire(activeView.filters),
        });
      } catch (e) {
        setError(`Rename failed: ${String((e as Error).message ?? e)}`);
      }
    }
  };

  const recentValues = useMemo(
    () =>
      Array.from(
        new Set(results.slice(0, 8).map((r) => r.trace_id.slice(0, 8))),
      ),
    [results],
  );

  const loadedLabel = `${results.length}${hasMore ? "+" : ""} result${
    results.length === 1 ? "" : "s"
  }`;

  // Fill most of the viewport below the header/filter/banner. Computed
  // once on mount — good enough without an AutoSizer dependency.
  const messagesListHeight = useMemo(
    () => (typeof window !== "undefined" ? Math.max(320, window.innerHeight - 430) : 520),
    [],
  );

  return (
    <div className="grid h-[calc(100vh-7rem)] grid-cols-[260px_1fr] gap-4">
      <SavedViewsRail
        views={views}
        activeId={activeId}
        onSelect={selectView}
        onCreate={onCreateView}
        onDelete={onDeleteView}
        integrationNameFor={(id) =>
          integrations.find((i) => i.id === id)?.name
        }
      />

      <div className="flex min-w-0 flex-col gap-3">
        {/* View header */}
        <header className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="text-xs uppercase tracking-wide text-muted">view</p>
            <h1 className="mt-1 truncate text-2xl font-semibold">
              {activeView?.name ?? "—"}
            </h1>
            <p className="mt-0.5 text-xs text-muted">
              {activeView ? (activeView.mine ? "yours" : "shared") : ""}
              {activeView?.lastEditedAt
                ? ` · edited ${formatRelative(activeView.lastEditedAt)}`
                : activeView
                  ? " · no edits yet"
                  : ""}
              {hasResults ? ` · ${loadedLabel}` : ""}
              {isDirty && (
                <span className="ml-2 font-medium" style={{ color: "var(--warn)" }}>
                  ● unsaved changes
                </span>
              )}
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <button
              type="button"
              className="btn"
              onClick={onRename}
              disabled={!activeView}
            >
              ✎ rename
            </button>
            <SharePermalinkButton
              path="/search"
              filters={draftFilters}
              range={windowVal}
              viewId={activeView && !isDraft(activeView) && !isDirty ? activeView.id : undefined}
            />
            <button
              type="button"
              className="btn"
              onClick={onReset}
              disabled={!isDirty}
            >
              ↶ reset
            </button>
            <button
              type="button"
              className="btn"
              onClick={() => setShowSaveAsNew(true)}
            >
              save as new
            </button>
            <button
              type="button"
              className="btn btn--primary"
              onClick={onSave}
              disabled={!isDirty || saving}
            >
              {saving ? "saving…" : "💾 save"}
            </button>
          </div>
        </header>

        <FilterEditor
          filters={draftFilters}
          onChange={setDraftFilters}
          knownIntegrations={integrations.map((i) => ({ id: i.id, name: i.name }))}
          recentValues={recentValues}
          fieldCatalog={fieldCatalog}
        />

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
              items={results}
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
                  {hasResults
                    ? "No messages match this view in the current time window."
                    : "Set up filters above to run a search."}
                </div>
              }
              header={
                <>
                  <span></span>
                  <span>time</span>
                  <span>msg id</span>
                  <span>integration · service</span>
                  <span>matched fields</span>
                  <span className="text-right">duration</span>
                  <span></span>
                </>
              }
              renderRow={(r) => (
                <>
                  <StatusPip kind={r.has_error ? "err" : "ok"} />
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
                  <span className="truncate">
                    {r.matched_service}
                    <span className="text-muted"> · {r.matched_span_name}</span>
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
                  <span
                    className="text-right text-xs"
                    style={{ color: "var(--primary)" }}
                  >
                    open ›
                  </span>
                </>
              )}
            />
          </div>

          {/* Footer */}
          <footer
            className="flex items-center justify-between border-t px-4 py-2 text-xs text-muted"
            style={{ borderColor: "var(--border)" }}
          >
            <span>
              {hasResults
                ? `${loadedLabel} loaded · scope: ${integrations.length} of ${integrations.length} integrations`
                : "Set up filters above to run a search."}
            </span>
            <div className="flex items-center gap-3">
              <button
                type="button"
                className="hover:underline disabled:opacity-50"
                disabled={loading}
                title="Re-run the current search"
                onClick={() => run()}
              >
                {loading ? "refreshing…" : "refresh"}
              </button>
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
      </div>

      <SaveAsViewDialog
        open={showSaveAsNew}
        filters={draftFilters}
        suggestedName={activeView ? `${activeView.name} (copy)` : undefined}
        onClose={() => setShowSaveAsNew(false)}
        onSubmit={onSaveAsNewSubmit}
      />

      <TraceDrawer traceId={openTraceId} onClose={() => setOpenTraceId(null)} />
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
    )
      return false;
  }
  return true;
}
