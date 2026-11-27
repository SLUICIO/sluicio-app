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
});
