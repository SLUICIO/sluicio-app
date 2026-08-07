// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// How one message's position reads on the flow graph (issue #15).
//
// The whole feature lives or dies on the wording here, so it is one
// tested function rather than a chain of ternaries inside JSX.
//
// The rule the copy has to hold to: say what was OBSERVED, and where
// nothing was observed, say what that means for where to look. A span
// only reaches storage once it has ENDED, so a node with no span might
// be busy, might have crashed before exporting, might never have been
// sent to. "Waiting here" would pick one of those and present it as
// fact. "Expected after order-api" says exactly as much as we know: the
// message got to order-api, this is what comes next, and it is not here.

import type { TraceNodeState, TraceNodeStateKind } from "../api/types";

export interface TraceNodeVisual {
  /** Short line under the service name. */
  label: string;
  /** Longer form for the title attribute. */
  detail: string;
  /** Which colour role the node border takes. */
  tone: "ok" | "err" | "next" | "idle";
  /** Faded — this node is not part of this message's story. */
  dimmed: boolean;
}

/** Local wall-clock time of an ISO instant, to the second. */
function clock(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime())
    ? ""
    : d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
}

const PLURAL = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

export function traceNodeVisual(s: TraceNodeState | undefined): TraceNodeVisual | null {
  if (!s) return null;
  const at = clock(s.first_seen);

  switch (s.state as TraceNodeStateKind) {
    case "failed":
      return {
        label: at ? `failed here · ${at}` : "failed here",
        detail:
          `The message reached this service and ${PLURAL(s.error_count, "span")} failed. ` +
          "This is usually where it stopped.",
        tone: "err",
        dimmed: false,
      };
    case "reached":
      return {
        label: at ? `reached · ${at}` : "reached",
        detail: `The message reached this service (${PLURAL(s.span_count, "span")}).`,
        tone: "ok",
        dimmed: false,
      };
    case "next":
      return {
        label: s.after_service ? `expected after ${s.after_service}` : "expected next",
        // Deliberately not "waiting here": nothing observed says it is.
        detail: s.after_service
          ? `The message reached ${s.after_service}, and this is what follows. ` +
            "No span has arrived from this service, so it either has not run, has not " +
            "finished, or never reported. This is where to look."
          : "The message has not arrived here, and it directly follows somewhere it reached.",
        tone: "next",
        dimmed: false,
      };
    default:
      return {
        label: "not reached",
        detail:
          "The message has no span here, and nothing directly upstream of it was reached either.",
        tone: "idle",
        dimmed: true,
      };
  }
}

/** Index a projection's nodes by service name for O(1) lookup. */
export function traceStatesByService(
  nodes: TraceNodeState[] | undefined,
): Record<string, TraceNodeState> {
  const out: Record<string, TraceNodeState> = {};
  (nodes ?? []).forEach((n) => {
    out[n.service_name] = n;
  });
  return out;
}

/**
 * The one-line answer shown above the graph.
 *
 * This is the sentence the feature exists to produce, so it is built
 * from the projection rather than assembled at the call site.
 */
export function traceSummaryLine(
  lastReached: string | undefined,
  nodes: TraceNodeState[] | undefined,
): string {
  const list = nodes ?? [];
  const failed = list.filter((n) => n.state === "failed");
  const next = list.filter((n) => n.state === "next");

  if (!lastReached) {
    return "This message has no spans on any service in this integration.";
  }
  if (failed.length > 0) {
    const names = failed.map((f) => f.service_name).join(", ");
    return `Failed at ${names}. Last activity was on ${lastReached}.`;
  }
  if (next.length === 0) {
    return `Reached ${lastReached}, which is the end of the flow as drawn.`;
  }
  const names = next.map((n) => n.service_name).join(", ");
  return `Last seen on ${lastReached}. Not yet on ${names}. That is where to look.`;
}
