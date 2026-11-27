// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { driftSummary, isDrifted } from "./collectorDrift";

const drift = (n: number) => ({
  deployment_id: "d",
  checked_at: "2026-11-28T00:00:00Z",
  paths: Array.from({ length: n }, (_, i) => ({
    path: `receivers.r${i}`,
    kind: (i % 2 ? "changed" : "missing") as "missing" | "changed",
  })),
});

describe("collector drift", () => {
  it("is nothing when in sync or not applicable", () => {
    expect(isDrifted(drift(0))).toBe(false);
    expect(isDrifted(undefined)).toBe(false);
    expect(driftSummary(drift(0))).toBe("");
  });

  it("names the paths, a few at most", () => {
    expect(driftSummary(drift(2))).toBe(
      "The collector's configuration changed since it was last rolled out from Sluicio: receivers.r0 is missing; receivers.r1 differs.",
    );
    expect(driftSummary(drift(7))).toMatch(
      /receivers\.r4 is missing, and 2 more\.$/,
    );
  });
});
