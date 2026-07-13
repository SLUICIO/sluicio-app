// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// checkLine — the one-line summary of a monitoring-template health check,
// shared by the Monitoring templates and System types pages so every
// signal renders identically everywhere (a per-page copy once showed
// trace checks as "metric · undefined undefined …").

import type { MonitoringTemplateCheck } from "../api/types";

export function checkLine(c: MonitoringTemplateCheck): string {
  if (c.signal === "log") {
    const sev = c.min_severity ? `severity≥${c.min_severity}` : "any severity";
    const body = c.body_contains ? ` body~"${c.body_contains}"` : "";
    return `log · ${sev}${body} · ≥${c.log_threshold ?? 1} in window`;
  }
  if (c.signal === "trace_error") {
    const attrs = (c.attrs ?? []).map((a) => `${a.key} ${a.op} ${a.value}`).join(", ");
    return `trace · ≥${c.trace_threshold ?? 1} failed traces in window${attrs ? ` [${attrs}]` : ""}`;
  }
  if (c.signal === "trace_latency") {
    return `trace · p95 latency ≥ ${c.threshold_ms ?? 0} ms`;
  }
  if (c.signal === "trace_volume") {
    return `trace · fewer than ${c.trace_threshold ?? 1} traces in window (dead-man)`;
  }
  // threshold (and friends) are omitempty server-side — a 0 threshold
  // arrives as undefined, so default the numerics explicitly.
  const split = c.split_by ? ` · split by ${c.split_by}` : "";
  return `metric · ${c.agg} ${c.metric} ${c.op} ${c.threshold ?? 0}${c.unit ? ` ${c.unit}` : ""}${split}`;
}
