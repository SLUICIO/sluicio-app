// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// TraceWaterfall — hop-level waterfall from the Sluicio handoff
// (variant A). One row per hop, with a status pip on the left, a
// positioned bar in a 0-to-total-ms ruler, and the duration on the
// right. Parallel hops overlap in time on the ruler.

import { useEffect, useRef, useState } from "react";
import type { SpanSummary } from "../api/types";
import { formatDurationMs } from "../lib/format";
import { StatusPip } from "./primitives";

interface Hop {
  span: SpanSummary;
  startMs: number;
  durationMs: number;
}

interface Props {
  spans: SpanSummary[];
  onHopClick?: (spanId: string) => void;
  selected?: string;
  // When true, the waterfall opens filtered to error spans only — used by
  // the trace drawer on an errored trace so the failing span isn't buried
  // under hundreds of healthy ones. The "Errors only" toggle still lets the
  // user expand to the full trace.
  defaultErrorsOnly?: boolean;
}

function hops(spans: SpanSummary[]): { hops: Hop[]; total: number; t0: number } {
  let t0 = Infinity;
  let tn = -Infinity;
  for (const s of spans) {
    const t = new Date(s.timestamp).getTime();
    if (t < t0) t0 = t;
    if (t + s.duration_ms > tn) tn = t + s.duration_ms;
  }
  if (!isFinite(t0) || !isFinite(tn) || tn <= t0) return { hops: [], total: 1, t0: 0 };
  const list: Hop[] = spans
    .slice()
    .sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime())
    .map((s) => ({
      span: s,
      startMs: new Date(s.timestamp).getTime() - t0,
      durationMs: s.duration_ms,
    }));
  return { hops: list, total: tn - t0, t0 };
}

export default function TraceWaterfall({ spans, onHopClick, selected, defaultErrorsOnly }: Props) {
  // "Errors only" filter — for long traces where the failing spans are
  // buried. We still derive total/t0 from ALL spans below, so the error
  // bars keep their true position on the full-trace timeline.
  const [errorsOnly, setErrorsOnly] = useState(defaultErrorsOnly ?? false);
  // Scroll the selected span into view (e.g. the auto-selected error span on
  // open) so it's visible without hunting through a long trace.
  const selectedRef = useRef<HTMLButtonElement | null>(null);
  useEffect(() => {
    selectedRef.current?.scrollIntoView({ block: "nearest" });
  }, [selected, errorsOnly]);
  const { hops: list, total } = hops(spans);
  if (list.length === 0) {
    return (
      <div className="p-6 text-sm text-muted">No spans in this trace.</div>
    );
  }

  const errorCount = list.filter((h) => h.span.status_code === "Error").length;
  // Show the toggle on any trace that has error spans. (It's a no-op when
  // every span errored, but staying visible there is less surprising than
  // vanishing on a fully-failed trace.)
  const showErrorsToggle = errorCount > 0;
  const visible = errorsOnly
    ? list.filter((h) => h.span.status_code === "Error")
    : list;

  // Ruler ticks at 0%, 25%, 50%, 75%, 100% of total.
  const ticks = [0, 0.25, 0.5, 0.75, 1].map((p) => ({
    pct: p,
    label: formatDurationMs(total * p),
  }));

  return (
    <div className="overflow-x-auto">
      {/* Errors-only toggle — shown only when filtering would actually
          help (some, but not all, spans errored). */}
      {showErrorsToggle && (
        <div
          className="flex items-center justify-between gap-3 border-b px-4 py-1.5 text-xs"
          style={{ borderColor: "var(--border)" }}
        >
          <span className="text-muted">
            {errorsOnly
              ? `Showing ${errorCount} error span${errorCount === 1 ? "" : "s"} of ${list.length}`
              : `${list.length} spans · ${errorCount} error${errorCount === 1 ? "" : "s"}`}
          </span>
          <label className="inline-flex cursor-pointer select-none items-center gap-1.5">
            <input
              type="checkbox"
              checked={errorsOnly}
              onChange={(e) => setErrorsOnly(e.target.checked)}
            />
            Errors only
          </label>
        </div>
      )}

      {/* Ruler */}
      <div
        className="grid items-center gap-3 border-b px-4 py-2 text-[11px] uppercase tracking-wide text-muted"
        style={{ gridTemplateColumns: "260px 1fr 90px", borderColor: "var(--border)" }}
      >
        <div>service · span</div>
        <div className="relative h-4">
          {ticks.map((t) => (
            <div
              key={t.pct}
              className="absolute -translate-x-1/2 text-muted"
              style={{ left: `${t.pct * 100}%` }}
            >
              {t.label}
            </div>
          ))}
        </div>
        <div className="text-right">duration</div>
      </div>

      {/* Hops */}
      {visible.map((h) => {
        const left = (h.startMs / total) * 100;
        const width = Math.max(0.3, (h.durationMs / total) * 100);
        const isError = h.span.status_code === "Error";
        const isSelected = selected === h.span.span_id;
        return (
          <button
            type="button"
            key={h.span.span_id}
            ref={isSelected ? selectedRef : null}
            onClick={() => onHopClick?.(h.span.span_id)}
            className="grid w-full items-center gap-3 px-4 py-2 text-left text-sm hover:bg-surface-elevated"
            style={{
              gridTemplateColumns: "260px 1fr 90px",
              borderBottom: "1px solid var(--border)",
              background: isSelected ? "var(--surface-3)" : undefined,
            }}
          >
            <div className="flex min-w-0 items-center gap-2">
              <StatusPip kind={isError ? "err" : "ok"} />
              <div className="min-w-0">
                <div className="truncate font-medium">{h.span.service_name}</div>
                <div className="truncate text-xs text-muted">{h.span.span_name}</div>
              </div>
            </div>
            <div className="relative h-5">
              <div
                className="absolute top-0 h-full rounded-sm"
                style={{
                  left: `${left}%`,
                  width: `${width}%`,
                  background: isError
                    ? "color-mix(in srgb, var(--err) 80%, transparent)"
                    : "color-mix(in srgb, var(--primary) 85%, transparent)",
                  border: `1px solid ${isError ? "var(--err)" : "var(--primary)"}`,
                }}
              >
                {h.durationMs > total * 0.08 && (
                  <span
                    className="ml-1 text-[11px] font-medium"
                    style={{ color: "white" }}
                  >
                    {formatDurationMs(h.durationMs)}
                  </span>
                )}
              </div>
            </div>
            <div className="text-right font-mono text-xs text-muted">
              {formatDurationMs(h.durationMs)}
            </div>
          </button>
        );
      })}
    </div>
  );
}
