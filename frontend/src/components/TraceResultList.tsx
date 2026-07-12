// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { Link } from "react-router-dom";
import { useTraceHref } from "../lib/traceHref";
import type { TraceSearchResult } from "../api/types";
import { formatDurationMs, formatNumber, formatRelative } from "../lib/format";
import { pickKeyEntries, useKeyAttributes } from "../lib/keyAttributes";

interface Props {
  traces: TraceSearchResult[];
  query: string;
}

/**
 * TraceResultList renders search results at the trace level — one
 * card per matching trace. Used by the global Search page and by the
 * service- and integration-scoped search inside their detail pages.
 *
 * Each card shows enough to identify the trace at a glance (matched
 * service + span name, duration, span count, error status, relative
 * time) and a row of key-attribute chips drawn from the matched
 * span's attributes so the value that drove the match is visible
 * without clicking through.
 */
export default function TraceResultList({ traces, query }: Props) {
  const keyAttrs = useKeyAttributes();
  return (
    <div className="result-list">
      {traces.map((t) => (
        <TraceCard key={t.trace_id} trace={t} query={query} keyAttrs={keyAttrs} />
      ))}
    </div>
  );
}

function TraceCard({
  trace,
  query,
  keyAttrs,
}: {
  trace: TraceSearchResult;
  query: string;
  keyAttrs: string[];
}) {
  const traceHref = useTraceHref();
  const attrs = trace.attributes ?? {};
  const keyEntries = pickKeyEntries(attrs, keyAttrs);
  const keySet = new Set(keyEntries.map(([k]) => k));
  // Surface up to a few extra attributes that explicitly matched the
  // user's query, in case the service type's key-attribute list
  // didn't include it (e.g. customer.id on an API span).
  const matched = Object.entries(attrs).filter(
    ([k, v]) => !keySet.has(k) && attrMatches(k, v, query)
  );

  return (
    <div className="result">
      <div className="result__top">
        <span className={`pill pill--${trace.has_error ? "errors" : "ok"}`}>
          {trace.has_error ? "Error" : "Ok"}
        </span>
        <Link
          className="result__service"
          to={`/services/${encodeURIComponent(trace.matched_service)}`}
        >
          {trace.matched_service}
        </Link>
        <span className="result__name">{trace.matched_span_name}</span>
        <span className="muted">{formatDurationMs(trace.duration_ms)}</span>
        <span className="muted">
          {formatNumber(trace.total_spans)} span{trace.total_spans === 1 ? "" : "s"} ·{" "}
          {formatNumber(trace.service_count)} service
          {trace.service_count === 1 ? "" : "s"}
        </span>
        <span className="muted">{formatRelative(trace.trace_start)}</span>
      </div>
      {keyEntries.length > 0 && (
        <div className="attrs attrs--key">
          {keyEntries.map(([k, v]) => (
            <span className="attr attr--key" key={k}>
              <span className="attr__k">{k}</span>
              <span className="attr__sep">=</span>
              <span className="attr__v">{v}</span>
            </span>
          ))}
        </div>
      )}
      {matched.length > 0 && (
        <div className="attrs">
          {matched.slice(0, 6).map(([k, v]) => (
            <span className="attr" key={k}>
              <span className="attr__k">{k}</span>
              <span className="attr__sep">=</span>
              <span className="attr__v">{v}</span>
            </span>
          ))}
        </div>
      )}
      <div className="result__actions">
        <Link className="btn btn--link" to={traceHref(trace.trace_id)}>
          Open trace →
        </Link>
        <span className="muted mono" style={{ fontSize: 12, marginLeft: 8 }}>
          {trace.trace_id.slice(0, 16)}…
        </span>
      </div>
    </div>
  );
}

function attrMatches(k: string, v: string, q: string): boolean {
  if (!q) return false;
  const needle = q.toLowerCase();
  return k.toLowerCase().includes(needle) || v.toLowerCase().includes(needle);
}
