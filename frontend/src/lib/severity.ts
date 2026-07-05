// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// OTLP severity helpers shared by the Logs surface. Maps the OTel
// severity_number (1–24) to the five-bucket badge used in the design:
// 1–8 → DBUG, 9–12 → INFO, 13–16 → WARN, 17–20 → ERR, 21–24 → FATAL.

export type SeverityBand = "debug" | "info" | "warn" | "err" | "fatal";

export function severityBand(num: number): SeverityBand {
  if (num >= 21) return "fatal";
  if (num >= 17) return "err";
  if (num >= 13) return "warn";
  if (num >= 9) return "info";
  return "debug";
}

export function levelBadgeLabel(num: number): string {
  switch (severityBand(num)) {
    case "fatal": return "FATAL";
    case "err": return "ERR";
    case "warn": return "WARN";
    case "info": return "INFO";
    default: return "DBUG";
  }
}

// Level threshold segments for the filter bar. `min` is the OTLP
// severity floor sent to the API (0 = no filter). `band` drives the
// active-segment tint.
export const LEVEL_SEGMENTS: { label: string; min: number; band: SeverityBand | null }[] = [
  { label: "All", min: 0, band: null },
  { label: "Debug", min: 5, band: "debug" },
  { label: "Info", min: 9, band: "info" },
  { label: "Warn", min: 13, band: "warn" },
  { label: "Error", min: 17, band: "err" },
  { label: "Fatal", min: 21, band: "fatal" },
];
