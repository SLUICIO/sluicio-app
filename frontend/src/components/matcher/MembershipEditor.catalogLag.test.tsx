// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A service that has only just started sending telemetry. The services
// list comes from the catalog, which is refreshed on a schedule, while a
// member's own preview reads the telemetry directly - so for a while the
// catalog does not know a service the preview has already counted. The
// editor used to trust the catalog: the row read "Not seen in the last
// 24h" beside "8 msgs", and the traces offered no neighbours for it.
// That is the first thing a new user does: send telemetry, then build an
// integration from it.

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Rule } from "../MatcherRules";

vi.mock("../../api/client", () => ({
  api: {
    listServices: () => Promise.resolve({ services: [] }),
    messageFields: () => Promise.resolve({ fields: [] }),
    previewIntegrationRules: () =>
      Promise.resolve({ services: ["order-intake"], service_count: 1, trace_count: 8, error_trace_count: 0 }),
    serviceNeighbors: () =>
      Promise.resolve({
        upstream: [],
        downstream: [{ service_name: "order-fulfillment", trace_count: 12, error_count: 0 }],
      }),
  },
}));

const MembershipEditor = (await import("./MembershipEditor")).default;

const member: Rule = { serviceOp: "equals", service: "order-intake", combine: "any", attrs: [] };

describe("a service the catalog has not caught up with", () => {
  it("is not called unseen while its own preview counts its traffic", async () => {
    render(<MembershipEditor rules={[member]} onChange={() => {}} combine="any" onCombineChange={() => {}} windowVal="24h" />);
    expect(await screen.findByText("8 msgs")).toBeTruthy();
    expect(screen.queryByText(/Not seen/)).toBeNull();
    expect(screen.getByRole("group", { name: "What this integration matches" }).textContent).toContain("Needs a look0");
  });

  it("still gets its neighbours suggested", async () => {
    render(<MembershipEditor rules={[member]} onChange={() => {}} combine="any" onCombineChange={() => {}} windowVal="24h" />);
    expect(await screen.findByRole("button", { name: /^Add order-fulfillment/ })).toBeTruthy();
  });
});
