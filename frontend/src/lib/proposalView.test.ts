// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { diffValue, isCreateProposal, proposalKindLabel, proposedRuleLines } from "./proposalView";

describe("proposal view", () => {
  it("names each kind", () => {
    expect(proposalKindLabel("alert_rule")).toBe("Tune a check");
    expect(proposalKindLabel("new_completion_rule")).toBe("New trace-completion rule");
    expect(isCreateProposal("new_alert_rule")).toBe(true);
    expect(isCreateProposal("integration_settings")).toBe(false);
  });

  it("reads a proposed check as a check", () => {
    const lines = proposedRuleLines("new_alert_rule", {
      name: "Orders: no traffic",
      severity: "warning",
      signal: "trace",
      integration_id: "i-1",
      trace_volume_spec: { kind: "trace_volume", threshold: 1, window_seconds: 900 },
    });
    expect(lines.some((l) => l.label === "On")).toBe(false);
    expect(lines.find((l) => l.label === "Fires when")?.value).toContain("fewer than 1 trace");
    expect(lines).toContainEqual({ label: "Notifies", value: "its notification profile" });
  });

  it("reads a proposed completion rule as its stages", () => {
    const lines = proposedRuleLines("new_completion_rule", {
      name: "Every receiver",
      start_span_name: "process orders",
      require: "any",
      stages: [
        { spans: [{ service: "order-publisher", span_name: "Step.Send" }], timeout_seconds: 30 },
        {
          spans: [
            { service: "erp-loader", span_name: "Step.Send" },
            { service: "wms-loader", span_name: "Step.Send" },
          ],
          require: "all",
          timeout_seconds: 60,
        },
      ],
      channel_ids: ["c-1"],
    });
    expect(lines).toContainEqual({ label: "Start span", value: "process orders" });
    expect(lines).toContainEqual({
      label: "Stage 2",
      value: "erp-loader: Step.Send + wms-loader: Step.Send (all of them) within 60s",
    });
    expect(lines).toContainEqual({ label: "Notifies", value: "1 channel" });
  });

  it("shows lists as blocks and scalars inline", () => {
    expect(diffValue(true)).toEqual({ text: "true", block: false });
    expect(diffValue(null)).toEqual({ text: "none", block: false });
    expect(diffValue(["production", "staging"])).toEqual({ text: "production, staging", block: false });
    expect(diffValue([])).toEqual({ text: "none", block: false });
    expect(diffValue([{ attribute: "service.name" }]).block).toBe(true);
  });
});
