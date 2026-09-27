// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A setup link points at /setup#t=<token>. Signed out, any path shows the
// sign-in page, and that is where the claim happens - so the moment it
// succeeds, the app renders its routes at /setup. With no route there,
// the first thing somebody saw on claiming their instance was "Page not
// found". This renders the real route table, signed in, at that address.

import { render, screen } from "@testing-library/react";
import { MemoryRouter, Outlet } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

// Signed in: the provider just renders what it gates.
vi.mock("./components/UserProvider", () => ({
  default: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("./components/AppShell", () => ({ default: () => <Outlet /> }));
vi.mock("./pages/Health", () => ({ default: () => <p>health page</p> }));
vi.mock("./pages/NotFound", () => ({ default: () => <p>page not found</p> }));

const App = (await import("./App")).default;

const at = (path: string) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  );

describe("the address a setup link points at", () => {
  it("lands on the dashboard once signed in, not on 'page not found'", async () => {
    at("/setup");
    expect(await screen.findByText("health page")).toBeTruthy();
    expect(screen.queryByText("page not found")).toBeNull();
  });

  // The control: the catch-all still catches, so the test above is
  // about /setup and not about a mock that renders the dashboard for
  // everything.
  it("still says 'page not found' for an address that is not one", async () => {
    at("/no-such-page");
    expect(await screen.findByText("page not found")).toBeTruthy();
  });
});
