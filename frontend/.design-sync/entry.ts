// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// design-sync entry barrel — the exact, scoped component set synced to
// claude.ai/design. Re-exports the shared primitives plus SeverityBadge
// (which lives outside the primitives barrel). esbuild bundles this into
// window.Sluicio.* for the design tool; helper fns (pipForStatus,
// attributeRows) and types are ignored by the converter's PascalCase scan.
export {
  StatusPip,
  Sparkline,
  Donut,
  KpiCard,
  SortableTh,
  KVTable,
  EditDrawer,
} from "../src/components/primitives";
export { default as SeverityBadge } from "../src/components/SeverityBadge";
