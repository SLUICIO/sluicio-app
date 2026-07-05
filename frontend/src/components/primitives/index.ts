// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shared design primitives used across the Sluicio pages. Mapped to
// the production design tokens (--bg, --surface, --border, --accent,
// --ok, --warning, --critical) — the wireframe-handoff used a
// hand-drawn aesthetic which has been deliberately discarded per
// the handoff README.

export { default as StatusPip } from "./StatusPip";
export type { PipKind } from "./StatusPip";
export { pipForStatus } from "./pipForStatus";
export { default as Sparkline } from "./Sparkline";
export { default as Donut } from "./Donut";
export { default as KpiCard } from "./KpiCard";
export { default as SortableTh } from "./SortableTh";
export { default as KVTable, attributeRows } from "./KVTable";
export type { KVRow } from "./KVTable";
export { default as EditDrawer } from "./EditDrawer";
