// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Donut — single-percentage donut chart with center label. Used in
// the success-rate KPI card on the dashboard and in error-breakdown
// inspector views.

interface Props {
  size?: number;
  pct: number; // 0..1
  label?: string;
  sub?: string;
  trackColor?: string;
  fillColor?: string;
  thickness?: number;
}

export default function Donut({
  size = 72,
  pct,
  label,
  sub,
  trackColor = "var(--surface-3)",
  fillColor = "var(--ok)",
  thickness = 8,
}: Props) {
  const r = size / 2 - thickness / 2;
  const c = 2 * Math.PI * r;
  const clamped = Math.max(0, Math.min(1, pct));
  const fillTone =
    clamped >= 0.97
      ? "var(--ok)"
      : clamped >= 0.9
        ? "var(--warn)"
        : "var(--err)";
  const actualFill = fillColor === "var(--ok)" ? fillTone : fillColor;

  return (
    <div style={{ position: "relative", width: size, height: size }}>
      <svg width={size} height={size} aria-hidden>
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          stroke={trackColor}
          strokeWidth={thickness}
        />
        <circle
          cx={size / 2}
          cy={size / 2}
          r={r}
          fill="none"
          stroke={actualFill}
          strokeWidth={thickness}
          strokeLinecap="round"
          strokeDasharray={`${c * clamped} ${c}`}
          transform={`rotate(-90 ${size / 2} ${size / 2})`}
        />
      </svg>
      <div
        style={{
          position: "absolute",
          inset: 0,
          display: "flex",
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          lineHeight: 1,
        }}
      >
        <div style={{ fontSize: size / 5, fontWeight: 600 }}>
          {label ?? `${Math.round(clamped * 100)}%`}
        </div>
        {sub && (
          <div style={{ fontSize: 10, color: "var(--muted)", marginTop: 2 }}>
            {sub}
          </div>
        )}
      </div>
    </div>
  );
}
