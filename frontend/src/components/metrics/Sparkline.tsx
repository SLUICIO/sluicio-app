// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A compact line+area sparkline for the metrics table. Draws the
// windowed series, an optional dashed threshold line, and a dot on the
// latest point. Colour signals state (breached → err, warn → warn, else
// primary). Pure SVG, no deps.

const W = 140;
const H = 32;
const P = 2;

export default function Sparkline({
  data,
  color = "var(--primary)",
  threshold,
}: {
  data: number[];
  color?: string;
  threshold?: number | null;
}) {
  if (!data || data.length < 2) {
    return <svg className="spark" width={W} height={H} viewBox={`0 0 ${W} ${H}`} aria-hidden />;
  }
  const max = Math.max(...data, threshold ?? -Infinity) * 1.1 || 1;
  const min = Math.min(...data) * 0.95;
  const span = max - min || 1;
  const xs = (i: number) => P + (i / (data.length - 1)) * (W - P * 2);
  const ys = (v: number) => H - P - ((v - min) / span) * (H - P * 2);
  const path = data.map((v, i) => `${i ? "L" : "M"} ${xs(i).toFixed(1)} ${ys(v).toFixed(1)}`).join(" ");
  const area = `${path} L ${xs(data.length - 1).toFixed(1)} ${H} L ${xs(0).toFixed(1)} ${H} Z`;
  const gradId = `spark-fade-${Math.round(min)}-${Math.round(max)}`;
  return (
    <svg className="spark" width={W} height={H} viewBox={`0 0 ${W} ${H}`} aria-hidden>
      <defs>
        <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={color} stopOpacity="0.18" />
          <stop offset="100%" stopColor={color} stopOpacity="0" />
        </linearGradient>
      </defs>
      {threshold != null && threshold <= max && threshold >= min && (
        <line
          x1={P}
          y1={ys(threshold)}
          x2={W - P}
          y2={ys(threshold)}
          stroke="var(--err)"
          strokeDasharray="3 3"
          strokeWidth="1"
          opacity="0.55"
        />
      )}
      <path d={area} fill={`url(#${gradId})`} />
      <path d={path} fill="none" stroke={color} strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />
      <circle cx={xs(data.length - 1)} cy={ys(data[data.length - 1])} r="2.4" fill={color} />
    </svg>
  );
}
