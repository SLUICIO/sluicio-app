// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Message trace — variant A (Path + waterfall) by default, with a
// right-side error panel (variant C) when the trace has at least
// one error span. The wireframe header carries id, integration,
// received timestamp, hop count, total duration, and a status pip.

import { useEffect, useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import IntegrationFlow from "../components/IntegrationFlow";
import TraceErrorPanel from "../components/TraceErrorPanel";
import TraceWaterfall from "../components/TraceWaterfall";
import TraceTrimPanel from "../components/traces/TraceTrimPanel";
import { KVTable, StatusPip, attributeRows } from "../components/primitives";
import type {
  LogEntry,
  SpanSummary,
  TraceCompletionFiring,
  TraceDetailResponse,
} from "../api/types";
import { formatDateTime, formatDurationMs } from "../lib/format";
import { pickKeyEntries, useKeyAttributes } from "../lib/keyAttributes";
import { usePageTitle } from "../lib/usePageTitle";
import { flowFromSpans } from "../lib/serviceFlow";

interface Range {
  start: number;
  end: number;
}

function traceRange(spans: SpanSummary[]): Range {
  let start = Infinity;
  let end = -Infinity;
  spans.forEach((s) => {
    const t = new Date(s.timestamp).getTime();
    if (t < start) start = t;
    const e = t + s.duration_ms;
    if (e > end) end = e;
  });
  if (!isFinite(start) || !isFinite(end) || end <= start) {
    return { start: 0, end: 1 };
  }
  return { start, end };
}

export default function TraceDetail() {
  const { traceId = "" } = useParams();
  // ?integration=<uuid> — when present, the trace's status is
  // scoped to that integration. A trace can participate in many
  // integrations; one being late doesn't make it late everywhere.
  const [searchParams] = useSearchParams();
  const integrationContextId = searchParams.get("integration") ?? undefined;
  usePageTitle(traceId ? `Trace · ${traceId.slice(0, 12)}` : "Trace");
  const [data, setData] = useState<TraceDetailResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [selectedSpan, setSelectedSpan] = useState<string | null>(null);
  const [integrationContextName, setIntegrationContextName] = useState<string | null>(null);
  const [trimOpen, setTrimOpen] = useState(false);
  const keyAttrs = useKeyAttributes();

  // Look up the integration's display name so the header can say
  // "as part of <name>" rather than a bare UUID.
  useEffect(() => {
    if (!integrationContextId) {
      setIntegrationContextName(null);
      return;
    }
    let cancelled = false;
    api
      .getIntegration(integrationContextId)
      .then((d) => {
        if (!cancelled) setIntegrationContextName(d.integration.name);
      })
      .catch(() => {
        /* fall through to UUID display on failure */
      });
    return () => {
      cancelled = true;
    };
  }, [integrationContextId]);

  useEffect(() => {
    setLoading(true);
    setError(null);
    api
      .traceDetail(traceId)
      .then(setData)
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  }, [traceId]);

  // Trace-completion firings on THIS trace. A warning-severity rule
  // flips the header pip to warn; critical flips it to err (same as
  // a real error span). Sticky-delayed: once a trace breaches its
  // SLA, it stays delayed even after the closing span lands — so
  // the pip stays loud until someone acknowledges or the firing
  // resolves manually.
  const [firings, setFirings] = useState<TraceCompletionFiring[]>([]);
  useEffect(() => {
    if (!traceId) return;
    let cancelled = false;
    const load = () =>
      api
        .listCompletionFiringsForTrace(traceId)
        .then((r) => {
          if (!cancelled) setFirings(r.firings);
        })
        .catch(() => {
          /* silent — pip just falls back to span-based status */
        });
    load();
    const t = window.setInterval(load, 30000);
    return () => {
      cancelled = true;
      window.clearInterval(t);
    };
  }, [traceId]);

  const range = useMemo(() => traceRange(data?.spans ?? []), [data]);
  const totalMs = range.end - range.start;

  const flow = useMemo(
    () => (data ? flowFromSpans(data.spans) : { nodes: [], edges: [] }),
    [data],
  );

  // For the flow graph: highlight the path the message took.
  const { pathNodes, pathEdges } = useMemo(() => {
    if (!data) return { pathNodes: new Set<string>(), pathEdges: new Set<string>() };
    const nodes = new Set<string>();
    const edges = new Set<string>();
    const byId = new Map(data.spans.map((s) => [s.span_id, s]));
    data.spans.forEach((s) => {
      nodes.add(s.service_name);
      if (s.parent_span_id) {
        const p = byId.get(s.parent_span_id);
        if (p && p.service_name !== s.service_name) {
          edges.add(`${p.service_name}|${s.service_name}`);
        }
      }
    });
    return { pathNodes: nodes, pathEdges: edges };
  }, [data]);

  const services = useMemo(() => {
    const m = new Map<string, number>();
    (data?.spans ?? []).forEach((s) => {
      m.set(s.service_name, (m.get(s.service_name) ?? 0) + 1);
    });
    return Array.from(m.entries()).map(([name, count]) => ({ name, count }));
  }, [data]);

  const errorSpans = useMemo(
    () => (data?.spans ?? []).filter((s) => s.status_code === "Error"),
    [data],
  );
  const hasError = errorSpans.length > 0;
  const primaryErrorSpan = errorSpans[0];

  // Active firings scoped to the current integration context (if
  // any). The status pip + alert banner only consider these so the
  // page can't disagree with itself depending on where the user
  // came from.
  const scopedFirings = useMemo(
    () =>
      firings.filter(
        (f) =>
          !integrationContextId || f.integration_id === integrationContextId,
      ),
    [firings, integrationContextId],
  );

  // Highest severity among ACTIVE firings on this trace. Resolved
  // firings don't change current status — they're history.
  const activeFiringSeverity = useMemo<
    "critical" | "warning" | "info" | null
  >(() => {
    let best: "critical" | "warning" | "info" | null = null;
    const rank = (s: string) =>
      s === "critical" ? 3 : s === "warning" ? 2 : s === "info" ? 1 : 0;
    for (const f of scopedFirings) {
      // Handled firings are benign — they no longer mark the trace
      // warning/error.
      if (f.state !== "firing" || f.handled_at) continue;
      const sev = f.severity;
      if (!best || rank(sev) > rank(best)) {
        best = sev;
      }
    }
    return best;
  }, [scopedFirings]);

  // Active firings on OTHER integrations — surfaced as a small
  // note so the user knows the trace is also late elsewhere even
  // if it's fine in the integration they're viewing.
  const otherIntegrationFiringCount = useMemo(() => {
    if (!integrationContextId) return 0;
    return firings.filter(
      (f) =>
        f.state === "firing" &&
        !f.handled_at &&
        f.integration_id !== integrationContextId,
    ).length;
  }, [firings, integrationContextId]);

  // Resolved firings in the current scope. Indicates "delivered
  // with delay" — the closing span eventually arrived past the
  // SLA. We keep them visible (rather than treating the trace as
  // a clean success) so operators retain the audit trail.
  const resolvedScopedFirings = useMemo(
    () => scopedFirings.filter((f) => f.state === "resolved"),
    [scopedFirings],
  );
  const hadResolvedFiring = resolvedScopedFirings.length > 0;

  // Combined header status. Order matters:
  //   1. real error span → err (unambiguous)
  //   2. critical firing → err (business-critical SLA breach)
  //   3. warning firing → warn (delayed but not yet escalated)
  //   4. resolved firing (no active) → ok pip but "delivered with
  //      delay" label so the audit trail stays visible
  //   5. otherwise → ok / "delivered"
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
        : hadResolvedFiring
          ? "delivered with delay"
          : "delivered";

  const selectedSpanRecord =
    (data?.spans ?? []).find((s) => s.span_id === selectedSpan) ?? null;

  return (
    <div className="flex flex-col gap-6">
      {/* Header */}
      <header className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <p className="text-xs uppercase tracking-wide text-muted">
            message trace
            {integrationContextId && (
              <>
                {" · status for "}
                <Link
                  to={`/integrations/${encodeURIComponent(integrationContextId)}`}
                  style={{ color: "var(--primary)" }}
                >
                  {integrationContextName ?? "integration"}
                </Link>
              </>
            )}
          </p>
          <h1 className="mt-1 truncate font-mono text-2xl">{traceId}</h1>
          {data && (
            <p className="mt-1 text-sm text-muted">
              {services.length} service{services.length === 1 ? "" : "s"} ·{" "}
              {data.spans.length} hop{data.spans.length === 1 ? "" : "s"} · total{" "}
              {formatDurationMs(totalMs)} · received{" "}
              {data.spans[0] && formatDateTime(data.spans[0].timestamp)}
            </p>
          )}
        </div>
        <div className="flex items-center gap-2">
          {data && data.spans.length > 0 && (
            <button className="btn btn--sm" type="button" onClick={() => setTrimOpen(true)} title="Generate a collector rule to drop these spans">
              ⚙ Trim ingestion
            </button>
          )}
          {data && <StatusPip kind={pipKind} label={pipLabel} />}
        </div>
      </header>

      {trimOpen && data && (
        <TraceTrimPanel spans={data.spans} onClose={() => setTrimOpen(false)} />
      )}

      {error && <div className="alert alert--error">{error}</div>}
      {loading && !data && <div className="placeholder">Loading…</div>}

      {activeFiringSeverity && (
        <div
          className={
            activeFiringSeverity === "critical"
              ? "alert alert--error"
              : "alert alert--warn"
          }
          style={{ marginBottom: 0 }}
        >
          <strong>SLA breach</strong>
          {integrationContextName && <> on {integrationContextName}</>} ·{" "}
          {scopedFirings
            .filter((f) => f.state === "firing" && !f.handled_at)
            .map((f) => f.rule_name)
            .join(", ")}
          {(() => {
            const first = scopedFirings.find(
              (f) => f.state === "firing" && !f.handled_at,
            );
            return first?.summary ? <> — {first.summary}</> : null;
          })()}
        </div>
      )}

      {!activeFiringSeverity && hadResolvedFiring && (
        // The trace was delayed at some point but the closing span
        // eventually arrived. The audit trail matters even though
        // the trace is now technically delivered — somebody waited.
        <div className="alert alert--info" style={{ marginBottom: 0 }}>
          <strong>Delivered with delay</strong>
          {integrationContextName && <> on {integrationContextName}</>} ·{" "}
          {resolvedScopedFirings
            .slice(0, 3)
            .map((f) => f.rule_name)
            .join(", ")}
          {(() => {
            const first = resolvedScopedFirings[0];
            return first?.summary ? <> — {first.summary}</> : null;
          })()}
        </div>
      )}

      {!activeFiringSeverity && otherIntegrationFiringCount > 0 && (
        // The trace is fine for the integration the user is viewing,
        // but is delayed on others. Surface that so they don't think
        // "delivered" applies everywhere — it doesn't.
        <div className="alert alert--warn" style={{ marginBottom: 0 }}>
          This trace is delivered for{" "}
          <strong>{integrationContextName ?? "this integration"}</strong> but
          breaches the SLA on {otherIntegrationFiringCount} other integration
          {otherIntegrationFiringCount === 1 ? "" : "s"}.{" "}
          <Link
            to={`/traces/${encodeURIComponent(traceId)}`}
            style={{ color: "var(--primary)" }}
          >
            View cross-integration status →
          </Link>
        </div>
      )}

      {data?.truncated && (
        <div className="alert alert--warn" style={{ marginBottom: 12 }}>
          This trace has more spans than the server-side cap. Showing
          the first <strong>{data.spans.length.toLocaleString()}</strong> by
          start time. A trace this large usually indicates a runaway loop
          or a missing span boundary in the producer.
        </div>
      )}

      {data && services.length > 0 && (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1.5fr_1fr]">
          {/* Left: flow + waterfall (and optionally logs from the error span) */}
          <div className="flex flex-col gap-4">
            <section
              className="overflow-hidden rounded-lg border bg-surface-2"
              style={{ borderColor: "var(--border)" }}
            >
              <div className="flex items-baseline justify-between border-b border-border px-4 py-3">
                <div>
                  <h2 className="text-base font-semibold">Flow · this message's path</h2>
                  <p className="text-xs text-muted">
                    Highlighted edges show the hops this message took.
                  </p>
                </div>
              </div>
              <div style={{ height: 320, position: "relative" }}>
                <IntegrationFlow
                  nodes={flow.nodes}
                  edges={flow.edges}
                  highlightPath={pathNodes}
                  highlightEdges={pathEdges}
                  onSelect={(name) => {
                    // Select the first span belonging to that service.
                    const span = data.spans.find((s) => s.service_name === name);
                    if (span) setSelectedSpan(span.span_id);
                  }}
                />
              </div>
            </section>

            <section
              className="overflow-hidden rounded-lg border bg-surface-2"
              style={{ borderColor: "var(--border)" }}
            >
              <div className="flex items-baseline justify-between border-b border-border px-4 py-3">
                <div>
                  <h2 className="text-base font-semibold">Waterfall</h2>
                  <p className="text-xs text-muted">Time spent at each hop. Click a row to inspect.</p>
                </div>
                <div className="text-xs text-muted">total · {formatDurationMs(totalMs)}</div>
              </div>
              <TraceWaterfall
                spans={data.spans}
                onHopClick={setSelectedSpan}
                selected={selectedSpan ?? undefined}
              />
            </section>

            <TraceLogsSection
              traceId={traceId}
              selectedSpanID={selectedSpan ?? undefined}
              selectedSpanLabel={
                selectedSpanRecord
                  ? `${selectedSpanRecord.service_name} · ${selectedSpanRecord.span_name}`
                  : undefined
              }
            />
          </div>

          {/* Right rail: the selected span's details sit here and are
              sticky, so they stay beside the waterfall as the user scrolls
              a long trace — instead of being pushed below it. Below the
              details: the error panel (or services list). */}
          <aside
            className="flex flex-col gap-4 lg:sticky lg:top-20"
            style={{ alignSelf: "start" }}
          >
            {selectedSpanRecord && (
              <SpanDetailsPanel
                span={selectedSpanRecord}
                keyAttrs={keyAttrs}
                onClose={() => setSelectedSpan(null)}
              />
            )}
            {hasError ? (
              <TraceErrorPanel errorSpan={primaryErrorSpan} />
            ) : (
              <section
                className="rounded-lg border bg-surface-2 p-4"
                style={{ borderColor: "var(--border)" }}
              >
                <h3 className="text-base font-semibold">Services in this trace</h3>
                <div className="mt-3 flex flex-wrap gap-1">
                  {services.map((s) => (
                    <Link key={s.name} className="badge" to={`/services/${encodeURIComponent(s.name)}`}>
                      {s.name}
                      <span className="muted"> · {s.count}</span>
                    </Link>
                  ))}
                </div>
              </section>
            )}

            {errorSpans.length > 1 && (
              <section
                className="rounded-lg border bg-surface-2 p-4"
                style={{ borderColor: "var(--border)" }}
              >
                <h3 className="text-base font-semibold">All error spans ({errorSpans.length})</h3>
                <ul className="mt-2 space-y-1 text-sm">
                  {errorSpans.map((s) => (
                    <li key={s.span_id}>
                      <button
                        type="button"
                        onClick={() => setSelectedSpan(s.span_id)}
                        className="text-left hover:underline"
                      >
                        <span className="font-medium">{s.service_name}</span>
                        <span className="text-muted"> · {s.span_name}</span>
                      </button>
                    </li>
                  ))}
                </ul>
              </section>
            )}
          </aside>
        </div>
      )}
    </div>
  );
}

// SpanDetailsPanel renders the selected span's attributes in the sticky
// right rail. It caps its own height and scrolls internally so a span
// with hundreds of attributes never pushes the sticky panel past the
// viewport (which would defeat the point of keeping it in view).
function SpanDetailsPanel({
  span,
  keyAttrs,
  onClose,
}: {
  span: SpanSummary;
  keyAttrs: string[];
  onClose: () => void;
}) {
  return (
    <section
      className="overflow-hidden rounded-lg border bg-surface-2"
      style={{
        borderColor: "var(--border)",
        display: "flex",
        flexDirection: "column",
        maxHeight: "calc(100vh - 96px)",
      }}
    >
      <div className="flex items-baseline justify-between border-b border-border px-4 py-3 gap-2">
        <div className="min-w-0">
          <h2 className="text-base font-semibold truncate">
            {span.service_name} <span className="text-muted">· {span.span_name}</span>
          </h2>
          <p className="text-xs text-muted">
            kind={span.span_kind} · duration={formatDurationMs(span.duration_ms)} ·{" "}
            status={span.status_code}
          </p>
        </div>
        <div className="flex items-center gap-2" style={{ flexShrink: 0 }}>
          <Link
            to={`/services/${encodeURIComponent(span.service_name)}`}
            className="text-sm hover:underline"
            style={{ color: "var(--primary)" }}
          >
            open service →
          </Link>
          <button
            type="button"
            className="btn btn--link"
            style={{ padding: 0, fontSize: 14 }}
            onClick={onClose}
            title="Close span details"
            aria-label="Close span details"
          >
            ✕
          </button>
        </div>
      </div>
      <div className="p-4" style={{ overflowY: "auto" }}>
        <KeyAttributes span={span} keyAttrs={keyAttrs} />
        <Attrs label="Resource attributes" attrs={span.resource_attributes} />
        <Attrs label="Span attributes" attrs={span.span_attributes} />
      </div>
    </section>
  );
}

function KeyAttributes({
  span,
  keyAttrs,
}: {
  span: SpanSummary;
  keyAttrs: string[];
}) {
  const entries = pickKeyEntries(span.attributes, keyAttrs);
  if (entries.length === 0) return null;
  return (
    <div className="attrs attrs--key">
      {entries.map(([k, v]) => (
        <span className="attr attr--key" key={k}>
          <span className="attr__k">{k}</span>
          <span className="attr__sep">=</span>
          <span className="attr__v">{v}</span>
        </span>
      ))}
    </div>
  );
}

function Attrs({ label, attrs }: { label: string; attrs?: Record<string, string> }) {
  if (!attrs || Object.keys(attrs).length === 0) return null;
  return (
    <div className="mt-3">
      <div className="kv__section">{label}</div>
      <KVTable rows={attributeRows(attrs)} />
    </div>
  );
}

// ── Logs for this trace ────────────────────────────────────────────────
//
// Pulls every log row whose TraceId matches this trace. Backend already
// supports `?trace_id=…` on /api/v1/logs (filter is exact-match against
// the indexed TraceId column on otel_logs). We fetch once on mount with
// a wide time range — the trace_id filter is so narrow that opening up
// the range is cheap, and traces can be hours or days old by the time
// someone opens them from search.
//
// When a span is selected in the waterfall, the user can flip on
// "Filter to this hop" which clips the rows to span_id === selectedSpanID
// in-memory. Cheaper than a refetch + keeps the toggle instant.

const TRACE_LOG_RANGE = "7d";
const TRACE_LOG_LIMIT = 500;

function TraceLogsSection({
  traceId,
  selectedSpanID,
  selectedSpanLabel,
}: {
  traceId: string;
  selectedSpanID?: string;
  selectedSpanLabel?: string;
}) {
  const [logs, setLogs] = useState<LogEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  // When true, narrow the displayed rows to the selected span. The
  // toggle is meaningless without a selected span, so we hide the
  // chip in that case.
  const [filterToSpan, setFilterToSpan] = useState(false);
  // Per-row expansion of attribute key/values.
  const [expanded, setExpanded] = useState<Set<string>>(new Set());

  useEffect(() => {
    if (!traceId) return;
    setLoading(true);
    setError(null);
    api
      .searchLogs(TRACE_LOG_RANGE, { traceId, limit: TRACE_LOG_LIMIT })
      .then((r) => setLogs(r.logs))
      .catch((e) => setError(String((e as Error).message ?? e)))
      .finally(() => setLoading(false));
  }, [traceId]);

  // When the user deselects the span (or it's never been selected),
  // make sure the filter toggle isn't sticky.
  useEffect(() => {
    if (!selectedSpanID) setFilterToSpan(false);
  }, [selectedSpanID]);

  const shown = useMemo(() => {
    if (!logs) return [];
    if (filterToSpan && selectedSpanID) {
      return logs.filter((l) => l.span_id === selectedSpanID);
    }
    return logs;
  }, [logs, filterToSpan, selectedSpanID]);

  return (
    <section
      className="overflow-hidden rounded-lg border bg-surface-2"
      style={{ borderColor: "var(--border)" }}
    >
      <div className="flex items-baseline justify-between border-b border-border px-4 py-3 gap-3">
        <div className="min-w-0">
          <h2 className="text-base font-semibold">Logs for this trace</h2>
          <p className="text-xs text-muted truncate">
            Correlated by TraceId. SDK trace-log propagation must be on for these to appear.
          </p>
        </div>
        <div className="flex items-center gap-3 text-xs">
          {selectedSpanID && (
            <label className="flex items-center gap-1.5 cursor-pointer" title={selectedSpanLabel}>
              <input
                type="checkbox"
                checked={filterToSpan}
                onChange={(e) => setFilterToSpan(e.target.checked)}
              />
              <span>Filter to selected hop</span>
            </label>
          )}
          {logs && (
            <span className="text-muted">
              {shown.length === logs.length
                ? `${logs.length} log${logs.length === 1 ? "" : "s"}`
                : `${shown.length} of ${logs.length}`}
            </span>
          )}
        </div>
      </div>

      {loading && <div className="placeholder">Loading logs…</div>}
      {error && <div className="alert alert--error">Failed to load logs: {error}</div>}

      {!loading && !error && logs && logs.length === 0 && (
        <div className="placeholder">
          No logs found correlated to this trace.
          <div className="muted text-xs mt-1">
            Logs need to carry the TraceId field — usually via your OpenTelemetry SDK's log appender or a tracer-aware logger.
          </div>
        </div>
      )}

      {!loading && !error && shown.length > 0 && (
        <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
          {shown.map((l, i) => {
            const id = `${l.timestamp}-${l.span_id ?? ""}-${i}`;
            const open = expanded.has(id);
            const allAttrs = {
              ...(l.resource_attributes ?? {}),
              ...(l.log_attributes ?? l.attributes ?? {}),
            };
            const hasAttrs = Object.keys(allAttrs).length > 0;
            return (
              <li
                key={id}
                style={{
                  borderTop: i === 0 ? "none" : "1px solid var(--border)",
                  padding: "8px 16px",
                  fontSize: 12.5,
                  lineHeight: 1.45,
                }}
              >
                <div style={{ display: "flex", gap: 10, alignItems: "baseline" }}>
                  <span
                    className="mono"
                    style={{ color: "var(--muted)", whiteSpace: "nowrap", flexShrink: 0 }}
                    title={new Date(l.timestamp).toLocaleString()}
                  >
                    {new Date(l.timestamp).toLocaleTimeString(undefined, { hour12: false })}
                    .{String(new Date(l.timestamp).getMilliseconds()).padStart(3, "0")}
                  </span>
                  <SeverityChip severity={l.severity_number} text={l.severity_text} />
                  <Link
                    to={`/services/${encodeURIComponent(l.service_name)}`}
                    className="mono"
                    style={{ color: "var(--ink-2)", flexShrink: 0 }}
                  >
                    {l.service_name}
                  </Link>
                  <span style={{ flex: 1, minWidth: 0, wordBreak: "break-word" }}>
                    {l.body}
                  </span>
                  {hasAttrs && (
                    <button
                      type="button"
                      className="btn btn--link"
                      style={{ fontSize: 11, flexShrink: 0 }}
                      onClick={() =>
                        setExpanded((prev) => {
                          const next = new Set(prev);
                          if (next.has(id)) next.delete(id);
                          else next.add(id);
                          return next;
                        })
                      }
                    >
                      {open ? "Hide attrs" : "Attrs"}
                    </button>
                  )}
                </div>
                {open && hasAttrs && (
                  <div style={{ marginTop: 6, marginLeft: 100 }}>
                    <KVTable rows={attributeRows(allAttrs)} />
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

// SeverityChip renders the OTel SeverityNumber as a colored chip
// matching the rest of the app's severity palette (Logs page reuses
// the same buckets).
function SeverityChip({ severity, text }: { severity: number; text: string }) {
  const label = (text || severityLabel(severity) || "INFO").toUpperCase();
  const bucket = severityBucket(severity);
  const colors: Record<string, { bg: string; ink: string }> = {
    error: { bg: "var(--err-soft)", ink: "var(--err-ink)" },
    warn: { bg: "var(--warn-soft)", ink: "var(--warn-ink)" },
    info: { bg: "var(--info-soft)", ink: "var(--info-ink)" },
    debug: { bg: "var(--surface-3)", ink: "var(--muted)" },
  };
  const c = colors[bucket];
  return (
    <span
      className="mono"
      style={{
        background: c.bg,
        color: c.ink,
        padding: "1px 6px",
        borderRadius: 4,
        fontSize: 10.5,
        fontWeight: 600,
        letterSpacing: "0.04em",
        flexShrink: 0,
      }}
    >
      {label}
    </span>
  );
}

function severityBucket(n: number): "error" | "warn" | "info" | "debug" {
  if (n >= 17) return "error";
  if (n >= 13) return "warn";
  if (n >= 5) return "info";
  return "debug";
}

function severityLabel(n: number): string {
  if (n >= 21) return "FATAL";
  if (n >= 17) return "ERROR";
  if (n >= 13) return "WARN";
  if (n >= 9) return "INFO";
  if (n >= 5) return "DEBUG";
  if (n >= 1) return "TRACE";
  return "";
}
