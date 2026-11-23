// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// CompletionCell is the completion column of an integration's message
// list: where each message stands against the integration's
// trace-completion rules. Complete once every stage was reached; waiting
// or delayed at the first stage that was not, inside or past its
// timeout. The same verdict the rule alerts on, so the list and the
// alerts agree.

import type { TraceCompletionStatus } from "../../api/types";

export function completionText(s: TraceCompletionStatus): string {
  switch (s.state) {
    case "complete":
      return "complete";
    case "waiting":
      return `waiting at ${s.stage_name || `stage ${s.stage}`}`;
    case "delayed":
      return `delayed at ${s.stage_name || `stage ${s.stage}`}`;
    default:
      return "not followed";
  }
}

export function completionTitle(s: TraceCompletionStatus): string {
  const where = s.stage ? ` Stage ${s.stage} of ${s.stages}${s.stage_name ? `: ${s.stage_name}` : ""}.` : "";
  switch (s.state) {
    case "complete":
      return `${s.rule_name}: every stage reached (${s.stages} of ${s.stages}).`;
    case "waiting":
      return `${s.rule_name}: ${s.reached} of ${s.stages} stages reached, still inside the timeout.${where}`;
    case "delayed":
      return `${s.rule_name}: ${s.reached} of ${s.stages} stages reached, past the timeout.${where}`;
    default:
      return `${s.rule_name}: this message never emitted the rule's start span, so the rule does not follow it.`;
  }
}

const STYLE: Record<TraceCompletionStatus["state"], string> = {
  complete: "pill pill--ok",
  waiting: "pill pill--quiet",
  delayed: "pill pill--errors",
  not_started: "",
};

export default function CompletionCell({ statuses, loading }: { statuses?: TraceCompletionStatus[]; loading?: boolean }) {
  if (loading) {
    return (
      <span className="text-xs text-muted" title="Reading where this message stands…">
        …
      </span>
    );
  }
  // Rules that do not follow this message say nothing worth a pill; a
  // message no rule follows shows a dash, like an absent attribute.
  const shown = (statuses ?? []).filter((s) => s.state !== "not_started");
  if (shown.length === 0) {
    const title = (statuses ?? []).map(completionTitle).join("\n") || "No trace-completion rule on this integration.";
    return (
      <span className="text-xs text-muted" title={title}>
        —
      </span>
    );
  }
  return (
    <span className="flex min-w-0 items-center gap-1 overflow-hidden">
      {shown.map((s) => (
        <span key={s.rule_id} className={`${STYLE[s.state]} truncate`} style={{ fontSize: 11 }} title={completionTitle(s)}>
          {completionText(s)}
        </span>
      ))}
    </span>
  );
}
