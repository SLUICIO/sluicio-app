// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The drawer's larger area chart of a metric's windowed series, with a
// dashed threshold line + label and time-axis ticks. Driven by the same
// sparkline samples as the table row (no extra fetch).

function hhmm(iso: string): string {
  const d = new Date(iso);
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}`;
}

export default function MetricChart({
  data,
  threshold,
  breached,
  fromISO,
  toISO,
}: {
  data: number[];
  threshold?: number | null;
  breached?: boolean;
  fromISO?: string;
  toISO?: string;
}) {
  const W = 480;
  const H = 150;
  const P = 14;
  const color = breached ? "var(--err)" : "var(--primary)";

  if (!data || data.length < 2) {
    return (
      <div className="mchart">
        <div className="placeholder" style={{ height: 120, margin: 8 }}>
          Not enough data points to chart.
        </div>
      </div>
    );
  }

  const max = Math.max(...data, threshold ?? -Infinity) * 1.12 || 1;
  const min = Math.min(...data) * 0.9;
  const span = max - min || 1;
  const xs = (i: number) => P + (i / (data.length - 1)) * (W - P * 2);
  const ys = (v: number) => H - P - ((v - min) / span) * (H - P * 2);
  const path = data.map((v, i) => `${i ? "L" : "M"} ${xs(i).toFixed(1)} ${ys(v).toFixed(1)}`).join(" ");
  const area = `${path} L ${xs(data.length - 1).toFixed(1)} ${H - P} L ${xs(0).toFixed(1)} ${H - P} Z`;
  const midISO =
    fromISO && toISO ? new Date((new Date(fromISO).getTime() + new Date(toISO).getTime()) / 2).toISOString() : "";

  return (
    <div className="mchart">
      <svg viewBox={`0 0 ${W} ${H}`} width="100%" height={H}>
        <defs>
          <linearGradient id="mchart-fade" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity="0.22" />
            <stop offset="100%" stopColor={color} stopOpacity="0" />
          </linearGradient>
        </defs>
        {[0.25, 0.5, 0.75].map((g) => (
          <line key={g} x1={P} y1={H * g} x2={W - P} y2={H * g} stroke="var(--border)" strokeDasharray="2 4" />
        ))}
        {threshold != null && threshold <= max && threshold >= min && (
          <g>
            <line
              x1={P}
              y1={ys(threshold)}
              x2={W - P}
              y2={ys(threshold)}
              stroke="var(--err)"
              strokeDasharray="4 3"
              strokeWidth="1"
            />
            <text
              x={W - P - 4}
              y={ys(threshold) - 4}
              textAnchor="end"
              style={{ fill: "var(--err)", font: "9px var(--mono)" }}
            >
              threshold {threshold}
            </text>
          </g>
        )}
        <path d={area} fill="url(#mchart-fade)" />
        <path d={path} fill="none" stroke={color} strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />
        <circle cx={xs(data.length - 1)} cy={ys(data[data.length - 1])} r="3" fill={color} />
        {fromISO && (
          <text x={P} y={H - 2} style={{ fill: "var(--muted)", font: "9px var(--mono)" }}>
            {hhmm(fromISO)}
          </text>
        )}
        {midISO && (
          <text x={W / 2} y={H - 2} textAnchor="middle" style={{ fill: "var(--muted)", font: "9px var(--mono)" }}>
            {hhmm(midISO)}
          </text>
        )}
        {toISO && (
          <text x={W - P} y={H - 2} textAnchor="end" style={{ fill: "var(--muted)", font: "9px var(--mono)" }}>
            {hhmm(toISO)}
          </text>
        )}
      </svg>
    </div>
  );
}
