// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// How a proposal reads in the inbox. A tuning proposal is a diff of a
// few scalars; a proposal that creates something carries the whole new
// rule, which a reviewer should read as a check or a pipeline, not as a
// line of JSON.

import type { AlertRule, TraceCompletionStage } from "../api/types";
import { alertCondition } from "./alertRule";
import { spanLabel, spansOf } from "./completionStages";

export function proposalKindLabel(kind: string): string {
  switch (kind) {
    case "alert_rule":
      return "Tune a check";
    case "new_alert_rule":
      return "New health check";
    case "new_completion_rule":
      return "New trace-completion rule";
    case "integration_settings":
      return "Integration settings";
    case "completion_rule":
      return "Change a completion rule";
    case "collector_config":
      return "Collector configuration";
    default:
      return kind.replace(/_/g, " ");
  }
}

/** A create proposal carries one change, the rule, with nothing before it. */
export function isCreateProposal(kind: string): boolean {
  return kind === "new_alert_rule" || kind === "new_completion_rule";
}

export interface SummaryLine {
  label: string;
  value: string;
}

/** The lines a reviewer reads for a proposed new check or completion rule. */
export function proposedRuleLines(kind: string, after: unknown): SummaryLine[] {
  if (!after || typeof after !== "object") return [];
  const r = after as Record<string, unknown>;
  const lines: SummaryLine[] = [];
  if (r.name) lines.push({ label: "Name", value: String(r.name) });
  if (r.severity) lines.push({ label: "Severity", value: String(r.severity) });

  if (kind === "new_alert_rule") {
    // The title names the integration or system; a service is named here.
    if (r.service_name) lines.push({ label: "On", value: `service ${String(r.service_name)}` });
    let condition = "";
    try {
      condition = alertCondition({ signal: "metric", ...r, spec: r.spec ?? {} } as unknown as AlertRule);
    } catch {
      condition = "";
    }
    if (condition) lines.push({ label: "Fires when", value: condition });
    const channels = Array.isArray(r.channel_ids) ? r.channel_ids.length : 0;
    lines.push({
      label: "Notifies",
      value: channels > 0 ? `${channels} channel${channels === 1 ? "" : "s"}` : "its notification profile",
    });
    if (r.runbook) lines.push({ label: "Runbook", value: String(r.runbook) });
  }

  if (kind === "new_completion_rule") {
    lines.push({ label: "Start span", value: r.start_span_name ? String(r.start_span_name) : "any trace" });
    const stages = Array.isArray(r.stages) ? (r.stages as TraceCompletionStage[]) : [];
    stages.forEach((st, i) => {
      const spans = spansOf(st).map(spanLabel);
      const all = st.require === "all" && spans.length > 1;
      const timeout = st.timeout_seconds ? ` within ${st.timeout_seconds}s` : "";
      lines.push({ label: `Stage ${i + 1}`, value: `${spans.join(all ? " + " : " / ")}${all ? " (all of them)" : ""}${timeout}` });
    });
    const channels = Array.isArray(r.channel_ids) ? r.channel_ids.length : 0;
    lines.push({
      label: "Notifies",
      value: channels > 0 ? `${channels} channel${channels === 1 ? "" : "s"}` : "its integration's notification profile",
    });
  }
  return lines;
}

/** A value in a diff: inline when short, a pretty block when structured. */
export function diffValue(v: unknown): { text: string; block: boolean } {
  if (v === null || v === undefined) return { text: "none", block: false };
  if (typeof v === "string") return { text: v, block: false };
  if (typeof v !== "object") return { text: String(v), block: false };
  // A list of names reads as a list: "production, staging".
  if (Array.isArray(v) && v.every((x) => x === null || typeof x !== "object")) {
    return { text: v.length === 0 ? "none" : v.map(String).join(", "), block: false };
  }
  return { text: JSON.stringify(v, null, 2), block: true };
}

/** A list of proposals as the inbox shows it: each on its own, or the
 *  proposals of one batch together, in the order they were filed. A
 *  batch stands where its newest item would. */
export type ProposalGroup<P> = { batch: null; items: [P] } | { batch: string; items: P[] };

export function groupProposals<P extends { batch_id?: string; batch_index?: number }>(list: P[]): ProposalGroup<P>[] {
  const out: ProposalGroup<P>[] = [];
  const batches = new Map<string, P[]>();
  for (const p of list) {
    if (!p.batch_id) {
      out.push({ batch: null, items: [p] });
      continue;
    }
    let items = batches.get(p.batch_id);
    if (!items) {
      items = [];
      batches.set(p.batch_id, items);
      out.push({ batch: p.batch_id, items });
    }
    items.push(p);
  }
  for (const items of batches.values()) items.sort((a, b) => (a.batch_index ?? 0) - (b.batch_index ?? 0));
  return out;
}
