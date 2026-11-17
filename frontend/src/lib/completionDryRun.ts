// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// What a completion rule's dry run says, in a line. The costly mistake
// is a start span no trace carries: the rule then follows nothing and
// never fires, and nothing on the page says so. The dry run makes that
// "0 of N traces" before the rule is saved.

import type { CompletionDryRun } from "../api/types";

export interface DryRunSummary {
  // "warn" when the rule as written would follow nothing.
  tone: "ok" | "warn";
  lines: string[];
}

export function summarizeDryRun(r: CompletionDryRun, startSpan: string, hours: number): DryRunSummary {
  const window = `the last ${hours} hours`;
  if (r.traces_on_services === 0) {
    return { tone: "warn", lines: [`The integration's services sent no traces in ${window}, so there is nothing to try it on.`] };
  }
  if (r.traces === 0) {
    return {
      tone: "warn",
      lines: [
        startSpan
          ? `No trace in ${window} carries the start span “${startSpan}” (the services sent ${r.traces_on_services}). As written, this rule would follow nothing.`
          : `No trace in ${window} to follow.`,
      ],
    };
  }
  const lines = [
    `Follows ${r.traces} of ${r.traces_on_services} traces in ${window}: ${r.completed} complete, ${r.pending} in flight, ${r.delayed} delayed.`,
  ];
  const reached = r.reached_stage.map((n, i) => `stage ${i + 1}: ${n}`).join(", ");
  if (reached) lines.push(`Reached ${reached}.`);
  const stuck = r.delayed_at_stage.findIndex((n) => n > 0);
  if (stuck >= 0 && r.delayed_samples.length > 0) {
    const s = r.delayed_samples.find((x) => x.stage === stuck + 1) ?? r.delayed_samples[0];
    lines.push(`Most delays wait at stage ${s.stage} for ${s.waiting_for}.`);
  }
  return { tone: r.completed === 0 && r.delayed > 0 ? "warn" : "ok", lines };
}
