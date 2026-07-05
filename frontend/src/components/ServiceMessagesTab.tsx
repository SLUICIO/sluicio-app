// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ServiceMessagesTab — the message search on a service detail page's
// Traces tab. It's the same FilterEditor experience as the global
// Messages page and the integration Messages tab, but the "service"
// filter is pre-set and locked to this service. Any filter combo can be
// saved as a view (scoped to the service) that then surfaces both here
// and in the global Messages rail — opening it there routes back to
// this service's Traces tab.
//
// Mirrors IntegrationMessages.tsx; the only differences are the locked
// field ("service" rather than "integration") and the saved-view scope
// ({ serviceId } rather than { integrationId }).

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api/client";
import FilterEditor, { type Filter } from "./search/FilterEditor";
import SaveAsViewDialog from "./search/SaveAsViewDialog";
import SharePermalinkButton from "./search/SharePermalinkButton";
import { StatusPip } from "./primitives";
import VirtualInfiniteList from "./VirtualInfiniteList";
import TraceDrawer from "./TraceDrawer";
import type {
  Integration,
  MessageCursor,
  MessageFilter,
  MessageView,
  TraceSearchResult,
} from "../api/types";
import type { SavedView } from "./search/types";
import { formatDateTime, formatDurationMs, formatRelative } from "../lib/format";
import {
  hydrateFiltersFromUrl,
  writeFiltersToParams,
} from "../lib/messageFilterUrl";
import { useTimeWindow } from "../lib/useTimeWindow";

// makeLockedServiceFilter builds the non-removable scope row. The
// search engine matches on the service name, so the value is the name.
function makeLockedServiceFilter(serviceName: string): Filter {
  return {
    id: "scope-service",
    field: "service",
    op: "is",
    value: serviceName,
    removable: false,
    locked: true,
  };
}

// filtersToWire mirrors the converter in Search.tsx / IntegrationMessages
// so all three surfaces serialize the same shape. Time is dropped — the
// range comes from the page-wide header selector.
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
    scope:
      v.scope?.integrationId || v.scope?.serviceId
        ? { integrationId: v.scope.integrationId, serviceId: v.scope.serviceId }
        : undefined,
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

interface Props {
  serviceName: string;
}

export default function ServiceMessagesTab({ serviceName }: Props) {
  const [windowVal] = useTimeWindow();

  // User-set filters (everything except the locked scope row, which is
  // prepended on the way to FilterEditor / the search call).
  const [userFilters, setUserFilters] = useState<Filter[]>(() =>
    hydrateFiltersFromUrl(window.location.search),
  );

  const [allViews, setAllViews] = useState<SavedView[]>([]);
  const [activeView, setActiveView] = useState<SavedView | null>(null);
  const [showOpenSaved, setShowOpenSaved] = useState(false);
  const [showSaveDialog, setShowSaveDialog] = useState(false);

  const [results, setResults] = useState<TraceSearchResult[]>([]);
  const [hasResults, setHasResults] = useState(false);
  const [nextCursor, setNextCursor] = useState<MessageCursor | undefined>(
    undefined,
  );
  const [hasMore, setHasMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const loadingRef = useRef(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [openTraceId, setOpenTraceId] = useState<string | null>(null);

  const PAGE = 100;
  const messagesListHeight = useMemo(
    () =>
      typeof window !== "undefined"
        ? Math.max(320, window.innerHeight - 540)
        : 420,
    [],
  );

  // Org integrations feed the FilterEditor's integration value picker
  // (for any extra integration row the user adds).
  const [integrations, setIntegrations] = useState<Integration[]>([]);
  useEffect(() => {
    api
      .listIntegrations(windowVal)
      .then((r) => setIntegrations(r.integrations ?? []))
      .catch(() => setIntegrations([]));
  }, [windowVal]);

  const lockedFilter = useMemo<Filter>(
    () => makeLockedServiceFilter(serviceName),
    [serviceName],
  );

  const composedFilters: Filter[] = useMemo(
    () => [lockedFilter, ...userFilters],
    [lockedFilter, userFilters],
  );

  // Load saved views — both views scoped to this service and global ones.
  useEffect(() => {
    api
      .listMessageViews()
      .then((r) => setAllViews((r.views ?? []).map(viewFromWire)))
      .catch(() => setAllViews([]));
  }, [serviceName]);

  const scopedViews = useMemo(
    () => allViews.filter((v) => v.scope?.serviceId === serviceName),
    [allViews, serviceName],
  );
  const globalViews = useMemo(
    () => allViews.filter((v) => !v.scope?.serviceId && !v.scope?.integrationId),
    [allViews],
  );

  // Keep ?q / ?s in sync with the user filters so the Traces tab is a
  // shareable deep link. Other params (?tab from ServiceDetail, ?range
  // from the header) are preserved.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    writeFiltersToParams(userFilters, params);
    const next = params.toString();
    const target = `${window.location.pathname}${next ? `?${next}` : ""}`;
    if (target !== `${window.location.pathname}${window.location.search}`) {
      window.history.replaceState(null, "", target);
    }
  }, [userFilters]);

  const run = useCallback(() => {
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
  }, [composedFilters, windowVal]);

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

  const onFiltersChange = (next: Filter[]) => {
    setUserFilters(next.filter((f) => !f.locked));
  };

  const isDirty = useMemo(() => {
    if (!activeView) return userFilters.length > 0;
    const viewUser = activeView.filters.filter((f) => !f.locked);
    return !filtersEqual(viewUser, userFilters);
  }, [activeView, userFilters]);

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
        shared: values.visibility !== "private",
        filters: filtersToWire(composedFilters),
        scope: { serviceId: serviceName },
      });
      const v = viewFromWire(created);
      setAllViews((curr) => [v, ...curr]);
      setActiveView(v);
      setShowSaveDialog(false);
    } catch (e) {
      setError(`Save failed: ${String((e as Error).message ?? e)}`);
    }
  };

  const recentValues = useMemo(
    () =>
      Array.from(new Set(results.slice(0, 8).map((r) => r.trace_id.slice(0, 8)))),
    [results],
  );

  const loadedLabel = `${results.length}${hasMore ? "+" : ""} result${
    results.length === 1 ? "" : "s"
  }`;

  return (
    <div className="flex flex-col gap-3">
      {/* Toolbar: open-saved / share / save-as-view — mirrors the
          integration Messages tab so the two feel identical. */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted">
          Messages in this service · same filters as the global Messages page,
          scoped to <span className="font-medium">{serviceName}</span>
        </p>
        <div className="flex items-center gap-2">
          <OpenSavedMenu
            open={showOpenSaved}
            onToggle={() => setShowOpenSaved((o) => !o)}
            onClose={() => setShowOpenSaved(false)}
            scopedViews={scopedViews}
            globalViews={globalViews}
            onSelect={loadView}
          />
          <SharePermalinkButton
            path={`/services/${encodeURIComponent(serviceName)}`}
            filters={userFilters}
            range={windowVal}
            extraParams={{ tab: "traces" }}
          />
          <button
            type="button"
            className="btn btn--primary"
            onClick={() => setShowSaveDialog(true)}
            title="Saves the current filter set as a view"
          >
            💾 save as view
          </button>
        </div>
      </div>

      {/* Active saved view banner. */}
      {activeView && (
        <div
          className="flex items-center justify-between rounded-md border bg-surface-2 px-3 py-2 text-xs"
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
        recentValues={recentValues}
      />

      {/* Save-as-view nudge — shown once the user adds a free filter. */}
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
              — available on this service's Traces tab and in the global Messages
              rail (scope: {serviceName}).
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
                  ? "No messages match in this service for the current filters."
                  : "Loading messages…"}
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

        <footer
          className="flex items-center justify-between border-t px-4 py-2 text-xs text-muted"
          style={{ borderColor: "var(--border)" }}
        >
          <span>
            {hasResults
              ? `${loadedLabel} loaded · scope: ${serviceName} only`
              : "Loading…"}
          </span>
        </footer>
      </section>

      <SaveAsViewDialog
        open={showSaveDialog}
        filters={composedFilters}
        scope={{ serviceId: serviceName }}
        onClose={() => setShowSaveDialog(false)}
        onSubmit={onSaveAsView}
      />

      <TraceDrawer traceId={openTraceId} onClose={() => setOpenTraceId(null)} />
    </div>
  );
}

// OpenSavedMenu — "open saved ▾" dropdown. Scoped views first, then
// global. Mirrors the integration Messages tab's menu.
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
                on this service
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
