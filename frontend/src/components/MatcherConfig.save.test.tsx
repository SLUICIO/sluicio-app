// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Saving what belongs to an integration is one request. It used to be an
// add per new row and a delete per old one, and a failure part-way left
// the integration matching both sets, or half of one.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

const calls = vi.hoisted(() => ({
  replaceMatchers: vi.fn((..._args: unknown[]) => Promise.resolve({ matchers: [], rule_match: "any" })),
  addMatcher: vi.fn(),
  removeMatcher: vi.fn(),
  updateIntegration: vi.fn(),
}));
vi.mock("../api/client", () => ({
  api: {
    ...calls,
    listServices: () => Promise.resolve({ services: [{ service_name: "order-gateway", status: "ok" }] }),
    messageFields: () => Promise.resolve({ fields: [] }),
    previewIntegrationRules: () => Promise.resolve({ services: [], service_count: 0 }),
    serviceNeighbors: () => Promise.resolve({ upstream: [], downstream: [] }),
  },
}));

const MatcherConfig = (await import("./MatcherConfig")).default;

const data = {
  integration: { id: "int-1", name: "Orders", description: "", rule_match: "any" },
  matchers: [
    { id: "m-1", attribute: "service.name", operator: "equals", value: "order-gateway", match_group: 0, include_descendants: false },
    { id: "m-2", attribute: "service.name", operator: "equals", value: "billing", match_group: 1, include_descendants: false },
  ],
} as never;

const view = () =>
  render(<MatcherConfig integrationId="int-1" data={data} canWrite windowVal="24h" onChanged={() => {}} />);

describe("saving what belongs to an integration", () => {
  it("is one request with the whole set, and nothing row by row", async () => {
    view();
    await userEvent.click(screen.getByRole("button", { name: "Remove billing" }));
    expect(screen.getByRole("region", { name: "Unsaved changes" }).textContent).toContain("removed billing");
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(calls.replaceMatchers).toHaveBeenCalledTimes(1);
    expect(calls.replaceMatchers.mock.calls[0]).toEqual([
      "int-1",
      { matchers: [{ attribute: "service.name", operator: "equals", value: "order-gateway", match_group: 0 }] },
    ]);
    expect(calls.addMatcher).not.toHaveBeenCalled();
    expect(calls.removeMatcher).not.toHaveBeenCalled();
    expect(calls.updateIntegration).not.toHaveBeenCalled();
  });

  // Sent only when it changed, so a save of the services alone can never
  // reset a mode somebody else set in the meantime.
  it("sends how the services combine only when it changed", async () => {
    calls.replaceMatchers.mockClear();
    view();
    await userEvent.click(screen.getByRole("button", { name: /Advanced matching/ }));
    await userEvent.click(screen.getByRole("radio", { name: /One trace through every service/ }));
    await userEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(calls.replaceMatchers.mock.calls[0][1]).toMatchObject({ rule_match: "all" });
  });
});
