// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Picks the integration the dashboard's "needs attention" KPI shows.
// A failing health check outranks raw error volume: an integration can
// log many error traces and still sit inside its thresholds (status
// "errors") while another is outright unhealthy — the unhealthy one is
// what the KPI must surface, matching the "N unhealthy" count next to
// it. Rank by status severity, then by failing services, then by error
// noise.

import type { Integration } from "../api/types";

function severity(i: Integration): number {
  return i.status === "unhealthy" ? 2 : i.status === "errors" ? 1 : 0;
}

export function pickNeedsAttention(integrations: Integration[]): Integration | undefined {
  const top = integrations
    .slice()
    .sort(
      (a, b) =>
        severity(b) - severity(a) ||
        (b.unhealthy_count ?? 0) - (a.unhealthy_count ?? 0) ||
        (b.error_trace_count ?? 0) - (a.error_trace_count ?? 0),
    )[0];
  // Only call it "needs attention" if there's actually something to pay
  // attention to in the current window. Otherwise the empty-state
  // ("All clear" / "No incidents in the current window") takes over.
  if (!top) return undefined;
  if (severity(top) === 0 && (top.error_trace_count ?? 0) === 0 && (top.unhealthy_count ?? 0) === 0) {
    return undefined;
  }
  return top;
}
