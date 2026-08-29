// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import ServiceRef from "./ServiceRef";

// The reach hook decides whether a service page is worth linking to.
const canOpen = vi.hoisted(() => ({ value: true }));
vi.mock("../lib/useNavigationReach", () => ({
  useCanOpenServices: () => canOpen.value,
}));

const wrap = (ui: React.ReactNode) => render(<MemoryRouter>{ui}</MemoryRouter>);

describe("ServiceRef", () => {
  it("links when the reader can open services", () => {
    canOpen.value = true;
    wrap(<ServiceRef name="orders-api" className="font-semibold underline hover:no-underline" />);
    const link = screen.getByRole("link", { name: "orders-api" });
    expect(link.className).toContain("underline");
  });

  // The point of the whole component: a reader who cannot open services
  // gets the NAME, not a thing dressed as a link that goes nowhere.
  it("renders plain text with no link styling when they cannot", () => {
    canOpen.value = false;
    wrap(<ServiceRef name="orders-api" className="font-semibold underline underline-offset-2 hover:no-underline" />);
    expect(screen.queryByRole("link")).toBeNull();
    const el = screen.getByTitle("orders-api");
    expect(el.className).toBe("font-semibold");
  });

  it("keeps classes that are not link affordances", () => {
    canOpen.value = false;
    wrap(<ServiceRef name="orders-api" className="mono muted" />);
    expect(screen.getByTitle("orders-api").className).toBe("mono muted");
  });

  it("drops the attribute entirely when nothing survives", () => {
    canOpen.value = false;
    wrap(<ServiceRef name="orders-api" className="underline hover:underline" />);
    expect(screen.getByTitle("orders-api").getAttribute("class")).toBeNull();
  });
});
