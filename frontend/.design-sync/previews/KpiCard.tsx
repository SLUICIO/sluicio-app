// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { KpiCard, Sparkline, Donut } from "@sluicio/frontend";

const frame: React.CSSProperties = {
  padding: 24,
  background: "var(--surface)",
  display: "grid",
  gridTemplateColumns: "repeat(2, minmax(200px, 240px))",
  gap: 16,
  fontFamily: "Inter, system-ui, sans-serif",
};

// The standard dashboard KPI tile: uppercase label, big tabular value,
// optional sub-line.
export const Basic = () => (
  <div style={frame}>
    <KpiCard label="Traces (1h)" value="12,481" sub="+8.2% vs prev hour" />
    <KpiCard label="Error rate" value="0.74%" tone="err" sub="92 failed traces" />
  </div>
);

// Emphasis variants — `selected` (active selection) and `attention`
// (needs-attention left rule). Never fill the whole card red.
export const Emphasis = () => (
  <div style={frame}>
    <KpiCard label="P95 latency" value="180 ms" emphasis="selected" sub="this service" />
    <KpiCard label="Open errors" value="3" tone="err" emphasis="attention" sub="unacknowledged" />
  </div>
);

// Compound — a KPI tile composing a Donut and a Sparkline in its body,
// the real dashboard layout.
export const WithVisuals = () => (
  <div style={frame}>
    <KpiCard label="Success rate" value="99.2%" tone="ok">
      <Donut pct={0.992} size={64} sub="24h" />
    </KpiCard>
    <KpiCard label="Throughput" value="3.4k/min" sub="last 15 min">
      <Sparkline data={[12, 18, 14, 22, 26, 19, 31, 28, 24, 35, 30, 41]} tone="default" stretch />
    </KpiCard>
  </div>
);
