// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Sparkline — minimal line chart suitable for dashboard cards.
// Accepts an array of numbers or undefined; renders a deterministic
// pseudo-random shape when data is missing so cards don't go blank
// before the API has loaded (the seed comes from the parent).

interface Props {
  data?: number[];
  seed?: number;
  // `width` defines the path coordinate space (the SVG viewBox). When
  // `stretch` is false (the default) it is also the rendered pixel
  // width. When `stretch` is true the SVG element fills its parent's
  // width via CSS while the path math stays inside this coordinate
  // space, so the line keeps its proportions but scales to any card.
  width?: number;
  height?: number;
  className?: string;
  tone?: "default" | "ok" | "warn" | "err" | "muted";
  showDots?: boolean;
  // Stretch the rendered SVG to 100% of its parent's width. Use this
  // on cards whose width is decided by a responsive grid (e.g. the
  // dashboard) so the sparkline doesn't end mid-card.
  stretch?: boolean;
}

const TONE_COLOR: Record<NonNullable<Props["tone"]>, string> = {
  default: "var(--primary)",
  ok: "var(--ok)",
  warn: "var(--warn)",
  err: "var(--err)",
  muted: "var(--muted)",
};

export default function Sparkline({
  data,
  seed = 1,
  width = 160,
  height = 40,
  className,
  tone = "default",
  showDots = false,
  stretch = false,
}: Props) {
  const values = data && data.length > 0 ? data : pseudo(seed, 24);
  const max = Math.max(...values, 0.0001);
  const min = Math.min(...values, 0);
  const range = max - min || 1;
  const stepX = values.length > 1 ? width / (values.length - 1) : width;

  const points = values.map((v, i) => {
    const x = i * stepX;
    const y = height - 3 - ((v - min) / range) * (height - 6);
    return [x, y] as const;
  });

  const d = points
    .map((p, i) => (i === 0 ? `M ${p[0]} ${p[1]}` : `L ${p[0]} ${p[1]}`))
    .join(" ");

  const area = `${d} L ${width} ${height} L 0 ${height} Z`;
  const color = TONE_COLOR[tone];

  return (
    <svg
      // When stretch is on, let CSS pick the rendered width so the SVG
      // fills its container; the viewBox keeps the path math intact and
      // preserveAspectRatio defaults to xMidYMid meet, which we don't
      // want — set it to none so the line spans edge-to-edge.
      width={stretch ? "100%" : width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio={stretch ? "none" : "xMidYMid meet"}
      className={className}
      style={{
        display: "block",
        overflow: "visible",
        ...(stretch ? { width: "100%" } : {}),
      }}
      aria-hidden
    >
      <path d={area} fill={color} opacity={0.12} />
      <path
        d={d}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
        // Keep a consistent pixel-thickness stroke even when the SVG is
        // stretched horizontally (preserveAspectRatio="none" would
        // otherwise scale the stroke non-uniformly and the line would
        // look chunky in wide cards).
        vectorEffect="non-scaling-stroke"
      />
      {showDots &&
        points.map((p, i) => (
          <circle key={i} cx={p[0]} cy={p[1]} r={1.5} fill={color} />
        ))}
    </svg>
  );
}

function pseudo(seed: number, n: number): number[] {
  const out: number[] = [];
  for (let i = 0; i < n; i++) {
    const v =
      Math.sin(seed * 9.7 + i * 1.3) * 0.5 +
      Math.cos(seed * 2.1 + i * 0.7) * 0.5 +
      1.2;
    out.push(Math.max(0.1, v));
  }
  return out;
}
