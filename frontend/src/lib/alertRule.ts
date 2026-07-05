// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Shared rendering helpers for alert_rules. The Alerts page (the single
// management surface) and the per-integration Settings page both list
// rules, so a rule's condition + signal badge read identically wherever
// it shows up.

import type { AlertRule } from "../api/types";

const OP_GLYPH: Record<string, string> = { gt: ">", gte: "≥", lt: "<", lte: "≤", eq: "=", neq: "≠" };

// logSevLabel maps an OTLP SeverityNumber floor to a human label.
export function logSevLabel(n: number): string {
  if (n >= 21) return "fatal";
  if (n >= 17) return "error";
  if (n >= 13) return "warn";
  return n > 0 ? `≥${n}` : "any";
}

// fmtWindow renders a trailing-window second-count compactly (e.g. "5m").
export function fmtWindow(seconds: number): string {
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

// alertCondition renders a rule's trigger condition, branching on signal
// (metric threshold vs log match/count vs failed traces).
export function alertCondition(rule: AlertRule): string {
  if (rule.signal === "trace" && rule.trace_latency_spec) {
    const s = rule.trace_latency_spec;
    const agg = s.aggregation === "max" ? "max" : "p95";
    return `${agg} response time ≥${s.threshold_ms}ms · ${fmtWindow(s.window_seconds)}`;
  }
  if (rule.signal === "trace" && rule.trace_volume_spec) {
    const s = rule.trace_volume_spec;
    return `fewer than ${s.threshold} trace${s.threshold === 1 ? "" : "s"} · ${fmtWindow(s.window_seconds)}`;
  }
  if (rule.signal === "trace" && rule.trace_error_spec) {
    const s = rule.trace_error_spec;
    return `≥${s.threshold} failed trace${s.threshold === 1 ? "" : "s"} · ${fmtWindow(s.window_seconds)}`;
  }
  if (rule.signal === "log" && rule.log_spec) {
    const s = rule.log_spec;
    // Direction-aware: "fewer than N" (drought) vs the default "≥ N" (flood).
    const below = s.comparison === "fewer_than";
    const parts = [below ? `fewer than ${s.threshold} logs` : `≥${s.threshold} logs`, `sev ${logSevLabel(s.min_severity)}`];
    if (s.body_contains) parts.push(`contains "${s.body_contains}"`);
    for (const a of s.attrs ?? []) parts.push(`${a.key} ${a.op} ${a.value}`);
    parts.push(fmtWindow(s.window_seconds));
    return parts.join(" · ");
  }
  const sp = rule.spec;
  const op = OP_GLYPH[sp.operator] ?? sp.operator;
  // Pushed checks carry no metric/aggregation — the value is fed in
  // externally and only the operator + threshold define a breach.
  if (rule.source === "pushed") {
    return `pushed value ${op} ${sp.threshold}${rule.unit ? ` ${rule.unit}` : ""}`;
  }
  const scope = (sp.attrs ?? []).length
    ? ` · ${(sp.attrs ?? []).map((a) => `${a.key}=${a.value}`).join(", ")}`
    : "";
  return `${sp.aggregation} ${sp.metric_name} ${op} ${sp.threshold} · ${sp.for_window}${scope}`;
}

// alertSignalLabel is the short badge shown next to a rule's condition.
// Metric rules carry no badge — they're the default signal.
export function alertSignalLabel(signal: string): string | null {
  if (signal === "log") return "log";
  if (signal === "trace") return "trace";
  return null;
}
