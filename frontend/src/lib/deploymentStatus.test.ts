// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { deploymentStatus } from "./deploymentStatus";

describe("deploymentStatus", () => {
  it("reads a pending proposal as waiting for approval", () => {
    const s = deploymentStatus({
      status: "proposed",
      proposal_state: "pending",
    });
    expect(s.label).toBe("Waiting for approval");
    expect(s.waiting).toBe(true);
  });

  it("does not leave a rejected or expired proposal waiting", () => {
    expect(
      deploymentStatus({ status: "proposed", proposal_state: "rejected" }),
    ).toMatchObject({
      label: "Not approved",
      waiting: false,
    });
    expect(
      deploymentStatus({ status: "proposed", proposal_state: "expired" })
        .waiting,
    ).toBe(false);
    expect(
      deploymentStatus({ status: "proposed", proposal_state: "superseded" })
        .label,
    ).toBe("Replaced by a newer proposal");
  });

  it("ignores the proposal once the deployment was released", () => {
    expect(
      deploymentStatus({ status: "applied", proposal_state: "approved" }).label,
    ).toBe("Applied");
  });

  it("says how long a temporary change has left, and how it ended", () => {
    const now = Date.parse("2026-11-27T12:00:00Z");
    expect(
      deploymentStatus(
        {
          status: "applied",
          duration_seconds: 3600,
          expires_at: "2026-11-27T12:42:00Z",
        },
        now,
      ).label,
    ).toBe("Applied for now, 42 min left");
    expect(
      deploymentStatus({ status: "ended", ended_reason: "expired" }).label,
    ).toBe("Ended: its time was up");
    expect(
      deploymentStatus({ status: "ended", ended_reason: "ended" }).label,
    ).toBe("Ended early");
    expect(deploymentStatus({ status: "held" }).label).toBe(
      "Waits for the temporary change to end",
    );
  });
});
