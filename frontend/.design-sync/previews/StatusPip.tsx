// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { StatusPip } from "@sluicio/frontend";

const frame: React.CSSProperties = {
  padding: 20,
  background: "var(--surface)",
  display: "flex",
  flexDirection: "column",
  gap: 12,
  fontFamily: "Inter, system-ui, sans-serif",
};

// Every status colour with its semantic label — the vocabulary used on
// service rows and health pills across Sluicio.
export const States = () => (
  <div style={frame}>
    <StatusPip kind="ok" label="Healthy" />
    <StatusPip kind="warn" label="Degraded" />
    <StatusPip kind="err" label="Down" />
    <StatusPip kind="muted" label="Unknown" />
  </div>
);

// Glyph-only — the compact form used inline in dense tables.
export const GlyphOnly = () => (
  <div style={{ ...frame, flexDirection: "row", gap: 16, alignItems: "center" }}>
    <StatusPip kind="ok" />
    <StatusPip kind="warn" />
    <StatusPip kind="err" />
    <StatusPip kind="muted" />
  </div>
);
