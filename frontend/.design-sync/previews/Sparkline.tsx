// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { Sparkline } from "@sluicio/frontend";

const frame: React.CSSProperties = {
  padding: 24,
  background: "var(--surface)",
  display: "flex",
  flexDirection: "column",
  gap: 18,
  fontFamily: "Inter, system-ui, sans-serif",
};

const row: React.CSSProperties = { display: "flex", alignItems: "center", gap: 14 };
const cap: React.CSSProperties = { fontSize: 12, color: "var(--muted)", width: 64 };

const throughput = [12, 18, 14, 22, 26, 19, 31, 28, 24, 35, 30, 27, 33, 41, 38];
const latency = [120, 118, 130, 126, 145, 138, 160, 152, 149, 171, 168, 180];

// One sparkline per semantic tone — the trend mini-charts that sit in
// dashboard KPI cards next to a headline number.
export const Tones = () => (
  <div style={frame}>
    <div style={row}><span style={cap}>traffic</span><Sparkline data={throughput} tone="default" width={200} /></div>
    <div style={row}><span style={cap}>success</span><Sparkline data={throughput} tone="ok" width={200} /></div>
    <div style={row}><span style={cap}>latency</span><Sparkline data={latency} tone="warn" width={200} /></div>
    <div style={row}><span style={cap}>errors</span><Sparkline data={[1, 0, 2, 1, 4, 3, 7, 6, 9, 8, 12, 11]} tone="err" width={200} /></div>
  </div>
);

// Dotted variant — emphasises individual samples on shorter series.
export const WithDots = () => (
  <div style={frame}>
    <Sparkline data={[8, 14, 9, 20, 16, 24, 19, 27]} tone="default" showDots width={220} height={56} />
  </div>
);
