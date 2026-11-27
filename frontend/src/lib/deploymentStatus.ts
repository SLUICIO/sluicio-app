// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// How a collector deployment reads in the UI. A proposed deployment
// whose proposal was rejected, expired or replaced is never touched by
// that decision, so its own status stays "proposed"; the proposal's state
// says what actually happened to it.

import type { CollectorDeployment } from "../api/types";
import type { PipKind } from "../components/primitives/StatusPip";

const STATUS: Record<string, { kind: PipKind; label: string }> = {
  proposed: { kind: "warn", label: "Waiting for approval" },
  pending: { kind: "muted", label: "Waiting for the collector" },
  sent: { kind: "muted", label: "Sent" },
  applying: { kind: "warn", label: "Applying" },
  applied: { kind: "ok", label: "Applied" },
  failed: { kind: "err", label: "Failed" },
  superseded: { kind: "muted", label: "Replaced before it applied" },
};

const CLOSED_PROPOSAL: Record<string, string> = {
  rejected: "Not approved",
  expired: "Expired without approval",
  superseded: "Replaced by a newer proposal",
};

export function deploymentStatus(
  d: Pick<CollectorDeployment, "status" | "proposal_state">,
): { kind: PipKind; label: string; waiting: boolean } {
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
