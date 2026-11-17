// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { summarizeDryRun } from "./completionDryRun";
import type { CompletionDryRun } from "../api/types";

const base: CompletionDryRun = {
  from: "",
  to: "",
  traces: 0,
  traces_on_services: 0,
  completed: 0,
  pending: 0,
  delayed: 0,
  reached_stage: [0, 0],
  delayed_at_stage: [0, 0],
  delayed_samples: [],
};

describe("summarizeDryRun", () => {
  it("says when the start span matches nothing", () => {
    const s = summarizeDryRun({ ...base, traces_on_services: 340 }, "Pipeline.orders.Send", 24);
    expect(s.tone).toBe("warn");
    expect(s.lines[0]).toContain("carries the start span “Pipeline.orders.Send” (the services sent 340)");
  });

  it("says when there is nothing to try it on", () => {
    expect(summarizeDryRun(base, "x", 24).lines[0]).toContain("sent no traces");
  });

  it("counts what it follows and where it gets stuck", () => {
    const s = summarizeDryRun(
      {
        ...base,
        traces: 120,
        traces_on_services: 340,
        completed: 110,
        pending: 4,
        delayed: 6,
        reached_stage: [118, 110],
        delayed_at_stage: [0, 6],
        delayed_samples: [{ trace_id: "t", stage: 2, waiting_for: "wms: Step.Send", stage_started_at: "" }],
      },
      "process orders",
      24,
    );
    expect(s.tone).toBe("ok");
    expect(s.lines).toEqual([
      "Follows 120 of 340 traces in the last 24 hours: 110 complete, 4 in flight, 6 delayed.",
      "Reached stage 1: 118, stage 2: 110.",
      "Most delays wait at stage 2 for wms: Step.Send.",
    ]);
  });
});
