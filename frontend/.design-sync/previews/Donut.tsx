// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { Donut } from "@integration-monitor/frontend";

const frame: React.CSSProperties = {
  padding: 24,
  background: "var(--surface)",
  display: "flex",
  gap: 28,
  alignItems: "center",
  fontFamily: "Inter, system-ui, sans-serif",
};

// Success-rate donuts — the fill auto-tones green / amber / red as the
// percentage crosses the 97% and 90% thresholds (the dashboard KPI use).
export const SuccessRates = () => (
  <div style={frame}>
    <Donut pct={0.992} sub="success" />
    <Donut pct={0.934} sub="success" />
    <Donut pct={0.815} sub="success" />
  </div>
);

// Larger donut with an explicit center label and a custom fill colour.
export const Labelled = () => (
  <div style={frame}>
    <Donut size={120} thickness={12} pct={0.68} label="6.8k" sub="of 10k quota" fillColor="var(--primary)" />
  </div>
);
