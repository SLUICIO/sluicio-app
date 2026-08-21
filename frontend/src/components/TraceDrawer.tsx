// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// TraceDrawer — a right-side slide-over that shows a trace's waterfall
// and the FULL attribute set for the selected span, without navigating
// away from the list it was opened from (e.g. the integration Messages
// tab). Deep-linking to the full /traces/:id page is still offered via
// the "open full view" link in the header.

import { useEffect, useMemo, useState } from "react";
import { useCanOpenServices } from "../lib/useNavigationReach";
import { Link } from "react-router-dom";
import { useTraceHref } from "../lib/traceHref";
import { traceOnFlowHref } from "../lib/traceOnFlowHref";
import { pickInitialSpan } from "../lib/spanSelection";
import { api } from "../api/client";
import TraceWaterfall from "./TraceWaterfall";
import { KVTable, StatusPip, attributeRows } from "./primitives";
import PromoteAttributeButton from "./integrations/PromoteAttributeButton";
import type {
  MessageColumn,
  SpanSummary,
  TraceCompletionFiring,
  TraceDetailResponse,
} from "../api/types";
import { formatDurationMs } from "../lib/format";

interface Props {
  traceId: string | null;
  onClose: () => void;
  // When the drawer is opened from inside an integration view, this
  // is the integration's id. The drawer scopes "delayed" status to
  // firings on rules belonging to this integration — a trace late
  // in integration A shouldn't visually flag here if the user is
  // looking at it through integration B.
  integrationContextId?: string;
  // Spans that satisfied the search this trace was opened from, oldest
  // first. The drawer opens on the first one that exists in the trace
  // and marks the rest, so "why did this come back?" is answered on
  // arrival instead of by reading the waterfall.
  //
  // Undefined/empty means the caller has no opinion (no span-level
  // predicate), and the default selection below applies unchanged.
  matchedSpanIds?: string[];
}

function traceTotalMs(spans: SpanSummary[]): number {
  let start = Infinity;
  let end = -Infinity;
  for (const s of spans) {
    const t = new Date(s.timestamp).getTime();
    if (t < start) start = t;
    if (t + s.duration_ms > end) end = t + s.duration_ms;
  }
  if (!isFinite(start) || !isFinite(end) || end <= start) return 0;
  return end - start;
}

export default function TraceDrawer({
  traceId,
  onClose,
  integrationContextId,
  matchedSpanIds,
}: Props) {
  const canOpenServices = useCanOpenServices();
  const [data, setData] = useState<TraceDetailResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [selectedSpan, setSelectedSpan] = useState<string | null>(null);
  const [firings, setFirings] = useState<TraceCompletionFiring[]>([]);
  // The integration's promoted columns, so each attribute row can show
  // whether it is already one (issue #23). Only meaningful when the
  // drawer was opened from inside an integration; a trace opened from
  // global Messages has no integration to promote INTO.
  const [msgColumns, setMsgColumns] = useState<MessageColumn[]>([]);
  // "open full view" forwards where the drawer was opened (path +
  // filters) so the full trace page can render a breadcrumb back to
  // the exact list the user came from.
  const traceHref = useTraceHref();

  // Fetch whenever the open trace changes.
  useEffect(() => {
    if (!traceId) {
      setData(null);
      setSelectedSpan(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    setData(null);
    setSelectedSpan(null);
    api
      .traceDetail(traceId)
      .then((d) => {
        if (cancelled) return;
        setData(d);
      })
      .catch((e) => !cancelled && setError(String(e.message ?? e)))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [traceId]);

  // Initial selection lives in its own effect rather than inside the
  // fetch above, so it can depend on matchedSpanIds honestly. Folding it
  // into the fetch meant either lying to the dependency array or
  // re-fetching the whole trace whenever the matched spans changed
  // identity — the selection is not a reason to hit the network.
  //
  // Neither dependency changes when the user clicks a span, so this does
  // not fight manual selection; it runs when a trace finishes loading.
  useEffect(() => {
    if (!data) return;
    setSelectedSpan(pickInitialSpan(data.spans, matchedSpanIds));
  }, [data, matchedSpanIds]);

  // The integration's current columns, so the attribute rows can show
  // which keys are already promoted. Fetched per open rather than
  // cached: another tab (or the settings page) may have changed them.
  useEffect(() => {
    if (!traceId || !integrationContextId) {
      setMsgColumns([]);
      return;
    }
    let cancelled = false;
    api
      .getIntegration(integrationContextId)
      .then((d) => !cancelled && setMsgColumns(d.integration.message_columns ?? []))
      .catch(() => !cancelled && setMsgColumns([]));
    return () => {
      cancelled = true;
    };
  }, [traceId, integrationContextId]);

  // Close on Escape.
  useEffect(() => {
    if (!traceId) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [traceId, onClose]);

  // Pull active firings on this trace. The drawer is always opened
  // from a specific integration's messages list, so we filter to
  // that integration's firings only — matching the row's pip in
  // the underlying list so the header doesn't disagree with what
  // the user just clicked.
  useEffect(() => {
    if (!traceId) {
      setFirings([]);
      return;
    }
    let cancelled = false;
    api
      .listCompletionFiringsForTrace(traceId)
      .then((r) => {
        if (!cancelled) setFirings(r.firings);
      })
      .catch(() => {
        /* silent — pip falls back to span-based status */
      });
    return () => {
      cancelled = true;
    };
  }, [traceId]);

  const totalMs = useMemo(() => traceTotalMs(data?.spans ?? []), [data]);
  const services = useMemo(() => {
    const set = new Set((data?.spans ?? []).map((s) => s.service_name));
    return set.size;
  }, [data]);
  const hasError = useMemo(
    () => (data?.spans ?? []).some((s) => s.status_code === "Error"),
    [data],
  );

  // Highest active firing severity, scoped to the integration the
  // drawer was opened from (when known). Without the context this
  // falls back to the worst severity across all integrations —
  // never zero, since the trace is meaningfully delayed somewhere.
  const activeFiringSeverity = useMemo<
    "critical" | "warning" | "info" | null
  >(() => {
    const rank = (s: string) =>
      s === "critical" ? 3 : s === "warning" ? 2 : s === "info" ? 1 : 0;
    let best: "critical" | "warning" | "info" | null = null;
    for (const f of firings) {
      if (f.state !== "firing") continue;
      if (integrationContextId && f.integration_id !== integrationContextId) continue;
      if (!best || rank(f.severity) > rank(best)) best = f.severity;
    }
    return best;
  }, [firings, integrationContextId]);

  // "Delivered with delay": no active firing in this context, but
  // there's a resolved one — the closing span eventually arrived
  // AFTER the SLA timeout. Distinguishes "fine" from "fine, but it
  // hurt" so operators can still see the breach happened.
  const hadResolvedFiring = useMemo(() => {
    return firings.some(
      (f) =>
        f.state === "resolved" &&
        (!integrationContextId || f.integration_id === integrationContextId),
    );
  }, [firings, integrationContextId]);

  const pipKind: "err" | "warn" | "ok" = hasError
    ? "err"
    : activeFiringSeverity === "critical"
      ? "err"
      : activeFiringSeverity === "warning"
        ? "warn"
        : "ok";
  const pipLabel = hasError
    ? "failed"
    : activeFiringSeverity === "critical"
      ? "delayed (critical)"
      : activeFiringSeverity === "warning"
        ? "delayed"
        : hadResolvedFiring && !activeFiringSeverity
          ? "delivered with delay"
          : "delivered";

  const selected = (data?.spans ?? []).find((s) => s.span_id === selectedSpan) ?? null;

  if (!traceId) return null;

  return (
    <div
      style={{ position: "fixed", inset: 0, zIndex: 60 }}
      role="dialog"
      aria-modal="true"
      aria-label="Trace detail"
    >
      {/* Backdrop */}
      <div
        onClick={onClose}
        style={{ position: "absolute", inset: 0, background: "rgba(15, 23, 42, 0.35)" }}
      />
      {/* Panel */}
      <aside
        className="flex flex-col bg-surface"
        style={{
          position: "absolute",
          top: 0,
          right: 0,
          bottom: 0,
          width: "min(720px, 92vw)",
          borderLeft: "1px solid var(--border)",
          boxShadow: "var(--shadow-pop, -8px 0 24px rgba(15, 23, 42, 0.18))",
        }}
      >
        {/* Header */}
        <div
          className="flex items-start justify-between gap-3 border-b px-4 py-3"
          style={{ borderColor: "var(--border)" }}
        >
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="text-xs uppercase tracking-wide text-muted">trace</span>
              {data && <StatusPip kind={pipKind} label={pipLabel} />}
            </div>
            <div className="mt-0.5 truncate font-mono text-sm">{traceId}</div>
            {data && (
              <div className="mt-0.5 text-xs text-muted">
                {services} service{services === 1 ? "" : "s"} · {data.spans.length} span
                {data.spans.length === 1 ? "" : "s"} · total {formatDurationMs(totalMs)}
              </div>
            )}
          </div>
          <div className="flex items-center gap-3">
            {/* "Where is this message right now" (issue #15). Offered
                only with an integration in context, because the answer
                is a position on THAT integration's flow — there is no
                such graph to project onto otherwise. */}
            {traceOnFlowHref(integrationContextId, traceId) && (
              <Link
                to={traceOnFlowHref(integrationContextId, traceId)!}
                className="whitespace-nowrap text-xs hover:underline"
                style={{ color: "var(--primary)" }}
                title="Show where this message got to on the integration's flow graph"
              >
                where is it? →
              </Link>
            )}
            <Link
              to={traceHref(traceId, integrationContextId, selectedSpan ?? undefined)}
              className="whitespace-nowrap text-xs hover:underline"
              style={{ color: "var(--primary)" }}
            >
              open full view →
            </Link>
            <button
              type="button"
              onClick={onClose}
              aria-label="Close"
              className="rounded px-2 py-0.5 text-lg leading-none hover:bg-surface-3"
            >
              ✕
            </button>
          </div>
        </div>

        {/* Body — split horizontally: the waterfall scrolls on top, and
            the selected span's details DOCK to the bottom so they stay in
            view without scrolling past a long waterfall (the drawer is too
            narrow for the side-by-side rail the full trace page uses). */}
        <div className="flex min-h-0 flex-1 flex-col">
          {error && <div className="alert alert--error m-3">{error}</div>}
          {loading && !data && <div className="px-4 py-6 text-sm text-muted">Loading trace…</div>}

          {data && data.spans.length > 0 && (
            <>
              {/* Waterfall — scrolls independently of the docked details */}
              <div className="min-h-0 flex-1 overflow-y-auto">
                <section
                  className="m-3 overflow-hidden rounded-lg border bg-surface-2"
                  style={{ borderColor: "var(--border)" }}
                >
                  <div className="border-b border-border px-4 py-2">
                    <h3 className="text-sm font-semibold">Waterfall</h3>
                    <p className="text-xs text-muted">Click a span to inspect its attributes.</p>
                  </div>
                  <TraceWaterfall
                    // Keyed per trace so the errors-only default re-applies
                    // when the drawer switches to another trace in place.
                    key={traceId}
                    spans={data.spans}
                    onHopClick={setSelectedSpan}
                    selected={selectedSpan ?? undefined}
                    matchedSpanIds={matchedSpanIds}
                    // Investigating from the Errors view: if the trace has a
                    // failing span, open filtered to it instead of the whole
                    // (possibly huge) trace.
                    defaultErrorsOnly={data.spans.some((s) => s.status_code === "Error")}
                  />
                </section>
              </div>

              {/* Selected span details — docked at the bottom, height-capped
                  with its own scroll so a span with many attributes never
                  shoves the waterfall off-screen. */}
              {selected && (
                <section
                  className="shrink-0 overflow-hidden border-t bg-surface-2"
                  style={{
                    borderColor: "var(--border)",
                    display: "flex",
                    flexDirection: "column",
                    maxHeight: "45%",
                  }}
                >
                  <div className="flex items-baseline justify-between border-b border-border px-4 py-2">
                    <div className="min-w-0">
                      <h3 className="truncate text-sm font-semibold">
                        {selected.service_name}{" "}
                        <span className="text-muted">· {selected.span_name}</span>
                      </h3>
                      <p className="text-xs text-muted">
                        kind={selected.span_kind} · duration={formatDurationMs(selected.duration_ms)} ·
                        status={selected.status_code}
                        {selected.status_message ? ` · ${selected.status_message}` : ""}
                      </p>
                    </div>
                    {/* Dropped entirely rather than rendered inert for a
                        reader who cannot open services (issue #32): this
                        is a call to action, and an inert one is just a
                        word taking up space. */}
                    {canOpenServices && (
                      <Link
                        to={`/services/${encodeURIComponent(selected.service_name)}`}
                        className="whitespace-nowrap text-xs hover:underline"
                        style={{ color: "var(--primary)" }}
                      >
                        service →
                      </Link>
                    )}
                  </div>
                  <div className="p-4" style={{ overflowY: "auto" }}>
                    <AttrTable
                      label="Span attributes"
                      attrs={selected.span_attributes}
                      integrationID={integrationContextId}
                      columns={msgColumns}
                      onColumnsChanged={setMsgColumns}
                    />
                    <AttrTable
                      label="Resource attributes"
                      attrs={selected.resource_attributes}
                      integrationID={integrationContextId}
                      columns={msgColumns}
                      onColumnsChanged={setMsgColumns}
                    />
                    {emptyAttrs(selected) && (
                      <div className="text-sm text-muted">No attributes on this span.</div>
                    )}
                  </div>
                </section>
              )}
            </>
          )}

          {data && data.spans.length === 0 && (
            <div className="px-4 py-6 text-sm text-muted">No spans found for this trace.</div>
          )}
        </div>
      </aside>
    </div>
  );
}

function emptyAttrs(span: SpanSummary): boolean {
  const a = Object.keys(span.span_attributes ?? {}).length;
  const b = Object.keys(span.resource_attributes ?? {}).length;
  return a + b === 0;
}

function AttrTable({
  label,
  attrs,
  integrationID,
  columns,
  onColumnsChanged,
}: {
  label: string;
  attrs?: Record<string, string>;
  // Absent when the drawer was opened outside an integration (global
  // Messages, Logs, Search). There is then no integration to promote
  // into, so the control is not rendered at all rather than rendered
  // disabled — an affordance you can never use is worse than none.
  integrationID?: string;
  columns: MessageColumn[];
  onColumnsChanged: (cols: MessageColumn[]) => void;
}) {
  if (!attrs || Object.keys(attrs).length === 0) return null;
  const rows = attributeRows(attrs);
  return (
    <div className="mt-2 first:mt-0">
      <div className="kv__section">
        {label} <span className="text-muted">· {rows.length}</span>
      </div>
      <KVTable
        rows={
          integrationID
            ? rows.map((r) => {
                const key = String(r.k);
                return {
                  ...r,
                  rowKey: key,
                  // The control rides in the KEY cell, which already
                  // accepts JSX for exactly this. Keeps the shared
                  // primitive free of a feature only this caller has.
                  k: (
                    <span style={{ display: "inline-flex", alignItems: "baseline", flexWrap: "wrap" }}>
                      <span>{key}</span>
                      <PromoteAttributeButton
                        attrKey={key}
                        value={attrs[key] ?? ""}
                        integrationID={integrationID}
                        columns={columns}
                        onChanged={onColumnsChanged}
                      />
                    </span>
                  ),
                };
              })
            : rows
        }
      />
    </div>
  );
}
