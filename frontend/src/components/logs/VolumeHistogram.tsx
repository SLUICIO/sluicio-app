// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The volume histogram: a compact stacked-bar SVG of log volume over
// the window, coloured by severity (info → warn → err → fatal, bottom
// to top). Drag across it to brush-select a sub-range, which refines
// the page time window. A context widget — the rows live below.

import { useMemo, useRef, useState } from "react";
import type { LogVolumeResponse } from "../../api/types";

const VB_W = 900;
const VB_H = 96;
const PAD_X = 8;
const TOP = 6;
const AXIS = 16; // reserved at the bottom for time-tick labels
const PLOT_BOTTOM = VB_H - AXIS;

function hhmm(iso: string): string {
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}`;
}
function fmt(n: number): string {
  return n.toLocaleString();
}

export default function VolumeHistogram({
  data,
  loading,
  onBrush,
}: {
  data: LogVolumeResponse | null;
  loading?: boolean;
  onBrush?: (fromISO: string, toISO: string) => void;
}) {
  const svgRef = useRef<SVGSVGElement | null>(null);
  const [drag, setDrag] = useState<{ x0: number; x1: number } | null>(null);

  // Memoised so the `?? []` fallback doesn't hand a fresh array identity
  // to the downstream useMemos on every render.
  const buckets = useMemo(() => data?.buckets ?? [], [data]);
  const totals = useMemo(() => {
    let info = 0, warn = 0, err = 0, fatal = 0;
    for (const b of buckets) {
      info += b.info;
      warn += b.warn;
      err += b.err;
      fatal += b.fatal;
    }
    return { info, warn, err, fatal, all: info + warn + err + fatal };
  }, [buckets]);

  const plotW = VB_W - PAD_X * 2;
  const n = buckets.length;
  const slot = n > 0 ? plotW / n : plotW;
  const barW = Math.max(1, slot - 1.5);
  const max = useMemo(
    () => Math.max(1, ...buckets.map((b) => b.info + b.warn + b.err + b.fatal)),
    [buckets],
  );
  const scale = (PLOT_BOTTOM - TOP) / max;

  // Map a clientX to an SVG-space x, then to a bucket index.
  const idxAt = (clientX: number): number => {
    const rect = svgRef.current?.getBoundingClientRect();
    if (!rect || n === 0) return 0;
    const svgX = ((clientX - rect.left) / rect.width) * VB_W;
    const i = Math.floor((svgX - PAD_X) / slot);
    return Math.min(Math.max(i, 0), n - 1);
  };

  const onDown = (e: React.MouseEvent) => {
    if (n === 0) return;
    const x = ((e.clientX - (svgRef.current!.getBoundingClientRect().left)) / svgRef.current!.getBoundingClientRect().width) * VB_W;
    setDrag({ x0: x, x1: x });
  };
  const onMove = (e: React.MouseEvent) => {
    if (!drag) return;
    const x = ((e.clientX - (svgRef.current!.getBoundingClientRect().left)) / svgRef.current!.getBoundingClientRect().width) * VB_W;
    setDrag({ ...drag, x1: x });
  };
  const onUp = (e: React.MouseEvent) => {
    if (!drag) return;
    // Resolve the bucket range from the recorded drag start + the
    // release point (both in client space).
    const lo = idxAt(dragClientX0.current);
    const hi = idxAt(e.clientX);
    const [i0, i1] = lo <= hi ? [lo, hi] : [hi, lo];
    setDrag(null);
    // A genuine drag spans ≥1 bucket; a click is a no-op.
    if (i1 > i0 && onBrush) {
      const fromISO = buckets[i0].start;
      const stepMs = (data?.step_seconds ?? 60) * 1000;
      const toISO =
        i1 + 1 < n ? buckets[i1 + 1].start : new Date(new Date(buckets[i1].start).getTime() + stepMs).toISOString();
      onBrush(fromISO, toISO);
    }
  };

  // Track the raw clientX at drag start (idxAt needs clientX, but state
  // holds svg-space x for drawing the overlay).
  const dragClientX0 = useRef(0);

  const legend = (label: string, count: number, varName: string, outline?: boolean) => (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 5, marginLeft: 12 }}>
      <span
        style={{
          width: 10, height: 10, borderRadius: 2,
          background: `var(${varName})`,
          outline: outline ? "1.5px solid var(--err)" : undefined,
          outlineOffset: outline ? 1 : undefined,
        }}
      />
      <span className="muted" style={{ fontSize: 12 }}>{label}</span>
      <span className="mono" style={{ fontSize: 11, color: "var(--muted)" }}>{fmt(count)}</span>
    </span>
  );

  return (
    <div className="card" style={{ padding: "10px 14px 4px" }}>
      <div style={{ display: "flex", alignItems: "baseline", justifyContent: "space-between", flexWrap: "wrap", gap: 8 }}>
        <div>
          <strong>{fmt(totals.all)}</strong>{" "}
          <span className="muted" style={{ fontSize: 12 }}>events</span>{" "}
          {data && (
            <span className="mono" style={{ fontSize: 11, color: "var(--muted)" }}>
              · {data.step_seconds}s buckets
            </span>
          )}
        </div>
        <div>
          {legend("info", totals.info, "--info-fill")}
          {legend("warn", totals.warn, "--warn")}
          {legend("error", totals.err, "--err")}
          {legend("fatal", totals.fatal, "--err", true)}
        </div>
      </div>

      <div style={{ position: "relative" }}>
        {loading && buckets.length === 0 ? (
          <div className="placeholder" style={{ height: 60, margin: "6px 0" }}>Loading volume…</div>
        ) : buckets.length === 0 ? (
          <div className="placeholder" style={{ height: 60, margin: "6px 0" }}>No volume in this window.</div>
        ) : (
          <svg
            ref={svgRef}
            viewBox={`0 0 ${VB_W} ${VB_H}`}
            // Stretch the viewBox to fill the element instead of letterboxing
            // it (the default xMidYMid meet centres the 900-wide content and
            // leaves side margins on wider containers). Without this the
            // clientX→viewBox mapping below — which maps across the full
            // element width — drifts from the cursor toward the edges, so the
            // brush box lands away from the mouse. Heights already match
            // (VB_H = rendered 96px), so only the X axis scales.
            preserveAspectRatio="none"
            width="100%"
            height={VB_H}
            style={{ cursor: "crosshair", userSelect: "none" }}
            onMouseDown={(e) => {
              dragClientX0.current = e.clientX;
              onDown(e);
            }}
            onMouseMove={onMove}
            onMouseUp={onUp}
            onMouseLeave={() => setDrag(null)}
          >
            {/* grid lines at 25/50/75% */}
            {[0.25, 0.5, 0.75].map((f) => {
              const y = TOP + (PLOT_BOTTOM - TOP) * (1 - f);
              return <line key={f} x1={PAD_X} x2={VB_W - PAD_X} y1={y} y2={y} stroke="var(--border)" strokeDasharray="2 4" />;
            })}

            {/* stacked bars */}
            {buckets.map((b, i) => {
              const x = PAD_X + i * slot;
              let yBottom = PLOT_BOTTOM;
              const seg = (count: number, fillVar: string, outline?: boolean) => {
                if (count <= 0) return null;
                const h = count * scale;
                yBottom -= h;
                return (
                  <rect
                    x={x}
                    y={yBottom}
                    width={barW}
                    height={h}
                    style={{ fill: `var(${fillVar})` }}
                    stroke={outline ? "var(--err)" : undefined}
                    strokeWidth={outline ? 0.75 : undefined}
                  />
                );
              };
              return (
                <g key={i}>
                  {seg(b.info, "--info-fill")}
                  {seg(b.warn, "--warn")}
                  {seg(b.err, "--err")}
                  {seg(b.fatal, "--err", true)}
                </g>
              );
            })}

            {/* brush overlay */}
            {drag && Math.abs(drag.x1 - drag.x0) > 2 && (
              <rect
                x={Math.min(drag.x0, drag.x1)}
                y={TOP}
                width={Math.abs(drag.x1 - drag.x0)}
                height={PLOT_BOTTOM - TOP}
                style={{ fill: "var(--primary-soft)", opacity: 0.55 }}
              />
            )}

            {/* axis ticks: start · mid · end */}
            <text x={PAD_X} y={VB_H - 4} style={{ fill: "var(--muted)", font: "10px 'JetBrains Mono', monospace" }}>{hhmm(buckets[0].start)}</text>
            <text x={VB_W / 2} y={VB_H - 4} textAnchor="middle" style={{ fill: "var(--muted)", font: "10px 'JetBrains Mono', monospace" }}>{hhmm(buckets[Math.floor(n / 2)].start)}</text>
            <text x={VB_W - PAD_X} y={VB_H - 4} textAnchor="end" style={{ fill: "var(--muted)", font: "10px 'JetBrains Mono', monospace" }}>{hhmm(buckets[n - 1].start)}</text>
          </svg>
        )}
      </div>
    </div>
  );
}
