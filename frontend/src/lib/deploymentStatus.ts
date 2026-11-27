// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// How a collector deployment reads in the UI. A proposed deployment
// whose proposal was rejected, expired or replaced is never touched by
// that decision, so its own status stays "proposed"; the proposal's state
// says what actually happened to it. A time-limited change says until
// when it runs, and how it ended.

import type { CollectorDeployment } from "../api/types";
import type { PipKind } from "../components/primitives/StatusPip";
import { formatDurationSeconds } from "./format";

const STATUS: Record<string, { kind: PipKind; label: string }> = {
  proposed: { kind: "warn", label: "Waiting for approval" },
  pending: { kind: "muted", label: "Waiting for the collector" },
  held: { kind: "muted", label: "Waits for the temporary change to end" },
  sent: { kind: "muted", label: "Sent" },
  applying: { kind: "warn", label: "Applying" },
  applied: { kind: "ok", label: "Applied" },
  failed: { kind: "err", label: "Failed" },
  superseded: { kind: "muted", label: "Replaced before it applied" },
  ended: { kind: "muted", label: "Ended" },
};

const CLOSED_PROPOSAL: Record<string, string> = {
  rejected: "Not approved",
  expired: "Expired without approval",
  superseded: "Replaced by a newer proposal",
};

// isActiveTemporary: a time-limited change that has not ended.
export function isActiveTemporary(
  d: Pick<CollectorDeployment, "status" | "duration_seconds">,
): boolean {
  return (
    !!d.duration_seconds &&
    ["pending", "sent", "applying", "applied"].includes(d.status)
  );
}

export function deploymentStatus(
  d: Pick<
    CollectorDeployment,
    | "status"
    | "proposal_state"
    | "duration_seconds"
    | "expires_at"
    | "ended_reason"
  >,
  now: number = Date.now(),
): { kind: PipKind; label: string; waiting: boolean } {
  if (d.status === "ended") {
    return {
      kind: "muted",
      label:
        d.ended_reason === "ended" ? "Ended early" : "Ended: its time was up",
      waiting: false,
    };
  }
  if (d.status === "applied" && d.duration_seconds && d.expires_at) {
    const left = Math.max(0, (Date.parse(d.expires_at) - now) / 1000);
    return {
      kind: "warn",
      label: `Applied for now, ${formatDurationSeconds(left)} left`,
      waiting: false,
    };
  }
  if (
    d.status === "proposed" &&
    d.proposal_state &&
    d.proposal_state !== "pending"
  ) {
    return {
      kind: "muted",
      label: CLOSED_PROPOSAL[d.proposal_state] ?? "Not approved",
      waiting: false,
    };
  }
  const st = STATUS[d.status] ?? { kind: "muted" as PipKind, label: d.status };
  return { ...st, waiting: d.status === "proposed" };
}
