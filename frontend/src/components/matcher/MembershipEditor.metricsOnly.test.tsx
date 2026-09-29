// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A member whose telemetry is metrics only: a broker read by a
// collector, one queue per integration. It has no messages, so a preview
// that counted messages alone reported every such member as matching
// nothing - "No traffic meets these conditions" beside a queue that was
// there - and could not tell that queue from a misspelled one.

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Rule } from "../MatcherRules";

const preview = vi.hoisted(() => ({ answer: {} as Record<string, unknown> }));
vi.mock("../../api/client", () => ({
  api: {
    listServices: () => Promise.resolve({ services: [{ service_name: "rabbitmq", status: "ok" }] }),
    messageFields: () => Promise.resolve({ fields: [] }),
    previewIntegrationRules: () => Promise.resolve(preview.answer),
    serviceNeighbors: () => Promise.resolve({ upstream: [], downstream: [] }),
  },
}));

const MembershipEditor = (await import("./MembershipEditor")).default;

const queue = (name: string): Rule => ({
  serviceOp: "equals",
  service: "rabbitmq",
  combine: "any",
  attrs: [{ attribute: "rabbitmq.queue.name", operator: "equals", value: name }],
});

const view = (rule: Rule) =>
  render(<MembershipEditor rules={[rule]} onChange={() => {}} combine="any" onCombineChange={() => {}} windowVal="24h" />);

describe("a member that only sends metrics", () => {
  it("counts its series, and is not told it matches nothing", async () => {
    preview.answer = { services: ["rabbitmq"], service_count: 1, trace_count: 0, error_trace_count: 0, metric_series: 2, metric_points: 26 };
    view(queue("invoices.outbound"));
    expect(await screen.findByText("2 series")).toBeTruthy();
    expect(screen.queryByText(/No traffic/)).toBeNull();
    const totals = screen.getByRole("group", { name: "What this integration matches" }).textContent ?? "";
    expect(totals).toContain("Metric series, 24h2");
    expect(totals).toContain("Needs a look0");
  });

  // The whole point of the preview: a typo is caught before saving.
  it("is still told when its conditions match nothing", async () => {
    preview.answer = { services: ["rabbitmq"], service_count: 1, trace_count: 0, error_trace_count: 0, metric_series: 0, metric_points: 0 };
    view(queue("invoices.outbond"));
    expect(await screen.findByText(/No traffic from rabbitmq meets these conditions/)).toBeTruthy();
  });
});
