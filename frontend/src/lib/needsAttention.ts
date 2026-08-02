// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Picks the integration the dashboard's "needs attention" KPI shows.
// A failing health check outranks raw error volume: an integration can
// log many error traces and still sit inside its thresholds (status
// "errors") while another is outright unhealthy — the unhealthy one is
// what the KPI must surface, matching the "N unhealthy" count next to
// it. Rank by status severity, then by failing services, then by error
// noise.

import type { Integration, System } from "../api/types";

function severity(i: Integration): number {
  return i.status === "unhealthy" ? 2 : i.status === "errors" ? 1 : 0;
}

/**
 * What the KPI is pointing at. Systems are peers of integrations
 * everywhere else in the product, and an unhealthy system is an incident
 * — but this KPI only ever looked at integrations, so a cell whose only
 * trouble was three unhealthy systems reported "All clear" while the
 * tile beside it said "3 of 3 unhealthy". Two panels, same screen,
 * opposite answers.
 */
export interface AttentionTarget {
  kind: "integration" | "system";
  id: string;
  name: string;
  /** Where the KPI's "open →" link goes. */
  href: string;
  errorTraceCount: number;
  unhealthyCount: number;
}

function systemSeverity(s: System): number {
  return s.status === "unhealthy" ? 2 : s.status === "errors" ? 1 : 0;
}

/**
 * Picks the single worst thing across integrations AND systems.
 *
 * Integrations win ties, because they carry error counts and so give the
 * reader more to act on; a system's severity is all it has.
 */
export function pickAttentionTarget(
  integrations: Integration[],
  systems: System[],
): AttentionTarget | undefined {
  const worstIntegration = pickNeedsAttention(integrations);
  const worstSystem = systems
    .slice()
    .sort((a, b) => systemSeverity(b) - systemSeverity(a))
    .find((s) => systemSeverity(s) > 0);

  const intSev = worstIntegration ? severity(worstIntegration) : 0;
  const sysSev = worstSystem ? systemSeverity(worstSystem) : 0;

  if (worstIntegration && intSev >= sysSev) {
    return {
      kind: "integration",
      id: worstIntegration.id,
      name: worstIntegration.name,
      href: `/integrations/${worstIntegration.id}`,
      errorTraceCount: worstIntegration.error_trace_count ?? 0,
      unhealthyCount: worstIntegration.unhealthy_count ?? 0,
    };
  }
  if (worstSystem) {
    return {
      kind: "system",
      id: worstSystem.id,
      name: worstSystem.name,
      href: `/systems/${worstSystem.id}`,
      errorTraceCount: 0,
      unhealthyCount: 0,
    };
  }
  return undefined;
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
