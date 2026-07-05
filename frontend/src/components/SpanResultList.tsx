// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { Link } from "react-router-dom";
import type { SpanSummary } from "../api/types";
import { formatDurationMs, formatRelative } from "../lib/format";
import { pickKeyEntries, useKeyAttributes } from "../lib/keyAttributes";
import { KVTable, attributeRows } from "./primitives";

interface Props {
  spans: SpanSummary[];
  query: string;
  showAdvanced?: boolean;
}

/**
 * SpanResultList renders search results identically wherever they appear —
 * the global Search page and the integration-scoped search inside an
 * integration detail. Keeping it in one component means future result-row
 * affordances (e.g. attribute filters, trace previews) only need to be
 * added once.
 */
export default function SpanResultList({ spans, query, showAdvanced = false }: Props) {
  const keyAttrs = useKeyAttributes();
  return (
    <div className="result-list">
      {spans.map((s) => (
        <SpanResult
          key={s.span_id}
          span={s}
          query={query}
          showAdvanced={showAdvanced}
          keyAttrs={keyAttrs}
        />
      ))}
    </div>
  );
}

function SpanResult({
  span,
  query,
  showAdvanced,
  keyAttrs,
}: {
  span: SpanSummary;
  query: string;
  showAdvanced: boolean;
  keyAttrs: string[];
}) {
  const attrs = span.attributes ?? {};
  // Two chip rows: first the "key" attributes for this span's
  // service type (file.name, http.route, etc), then the ones that
  // matched the user's search query — minus any duplicates.
  const keyEntries = pickKeyEntries(attrs, keyAttrs);
  const keySet = new Set(keyEntries.map(([k]) => k));
  const matched = Object.entries(attrs).filter(
    ([k, v]) => !keySet.has(k) && attrMatches(k, v, query)
  );

  return (
    <div className="result">
      <div className="result__top">
        <span className={`pill pill--${span.status_code === "Error" ? "errors" : "ok"}`}>
          {span.status_code}
        </span>
        <Link className="result__service" to={`/services/${encodeURIComponent(span.service_name)}`}>
          {span.service_name}
        </Link>
        <span className="result__name">{span.span_name}</span>
        <span className="muted">{formatDurationMs(span.duration_ms)}</span>
        <span className="muted">{formatRelative(span.timestamp)}</span>
      </div>
      {span.status_message && <div className="result__msg">{span.status_message}</div>}
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
        <Link className="btn btn--link" to={`/traces/${span.trace_id}`}>
          View full trace →
        </Link>
      </div>
      {showAdvanced && (
        <details className="result__details" open>
          <summary>Span details</summary>
          <KVTable
            rows={[
              { k: "trace_id", v: span.trace_id, copyValue: span.trace_id },
              { k: "span_id", v: span.span_id, copyValue: span.span_id },
              { k: "kind", v: span.span_kind, copyValue: span.span_kind },
            ]}
          />
          {span.resource_attributes && Object.keys(span.resource_attributes).length > 0 && (
            <>
              <div className="kv__section">Resource attributes</div>
              <KVTable rows={attributeRows(span.resource_attributes)} />
            </>
          )}
          {span.span_attributes && Object.keys(span.span_attributes).length > 0 && (
            <>
              <div className="kv__section">Span attributes</div>
              <KVTable rows={attributeRows(span.span_attributes)} />
            </>
          )}
        </details>
      )}
    </div>
  );
}

function attrMatches(k: string, v: string, q: string): boolean {
  if (!q) return false;
  const needle = q.toLowerCase();
  return k.toLowerCase().includes(needle) || v.toLowerCase().includes(needle);
}
