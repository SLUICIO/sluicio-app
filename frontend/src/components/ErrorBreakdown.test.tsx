// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

const canOpen = vi.hoisted(() => ({ value: true }));
vi.mock("../lib/useNavigationReach", () => ({
  useCanOpenServices: () => canOpen.value,
}));
vi.mock("../lib/useCurrentUser", () => ({
  useCurrentUser: () => ({ can: () => false }),
}));
vi.mock("../api/client", () => ({
  // The response IS the attribution, not a wrapper around it. dimension
  // "service" short-circuits the by-dimension branch, so the per-service
  // view under test is the one that renders.
  api: {
    integrationErrorBreakdown: () => Promise.resolve({ dimension: "service", buckets: [] }),
  },
}));

const ErrorBreakdown = (await import("./ErrorBreakdown")).default;

const services = [
  {
    service_name: "order-fulfillment",
    error_trace_count: 43,
    status: "errors",
    facets: ["Queue input", "HTTP output"],
  },
] as never;

const view = () =>
  render(
    <MemoryRouter>
      <ErrorBreakdown integrationId="abc" services={services} />
    </MemoryRouter>,
  );

describe("ErrorBreakdown without service access", () => {
  // The grant withheld services as objects. Naming the failing one, and
  // what it does, hands back exactly that — and points nowhere the
  // reader can follow.
  it("names no service anywhere", async () => {
    canOpen.value = false;
    view();
    expect(await screen.findByText(/failed trace/i)).toBeTruthy();
    expect(screen.queryByText(/order-fulfillment/)).toBeNull();
    expect(screen.queryByText(/Queue input/)).toBeNull();
    expect(screen.queryByText(/of failures/)).toBeNull();
    expect(screen.queryByText(/across 1 service/)).toBeNull();
  });

  // Suppressing the attribution must not strip the way OUT. The panel's
  // job is still to answer "where are the error traces".
  it("still offers the route to the failed messages", async () => {
    canOpen.value = false;
    view();
    expect(await screen.findByText(/see all 43 failed/)).toBeTruthy();
  });

  it("keeps the attribution for a reader who can open services", async () => {
    canOpen.value = true;
    view();
    // Named in the callout AND in the breakdown row, hence findAll.
    expect((await screen.findAllByText(/order-fulfillment/)).length).toBeGreaterThan(0);
    expect(screen.getByText(/of failures/)).toBeTruthy();
  });
});
