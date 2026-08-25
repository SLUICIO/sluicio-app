// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";
import TabStrip from "./TabStrip";

const wrap = (ui: React.ReactNode) => render(<MemoryRouter>{ui}</MemoryRouter>);

describe("TabStrip", () => {
  it("renders a link when the item navigates and a button when it does not", () => {
    wrap(
      <TabStrip
        ariaLabel="x"
        items={[
          { key: "a", label: "Routed", to: "/somewhere", active: true },
          { key: "b", label: "Stateful", onClick: () => {}, active: false },
        ]}
      />,
    );
    expect(screen.getByRole("link", { name: "Routed" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Stateful" })).toBeTruthy();
  });

  // The active tab is the underline plus the 14px/700 text. A `border` or
  // `font` shorthand in the button branch resets those longhands whatever
  // order it is written in, which once left the button at the browser's
  // 16px/400 default with no underline while the Link beside it was fine.
  it("gives a button tab the same active styling as a link tab", () => {
    wrap(
      <TabStrip
        ariaLabel="x"
        items={[
          { key: "a", label: "Routed", to: "/a", active: true },
          { key: "b", label: "Stateful", onClick: () => {}, active: true },
        ]}
      />,
    );
    const link = screen.getByRole("link", { name: "Routed" });
    const button = screen.getByRole("button", { name: "Stateful" });
    for (const prop of ["fontSize", "fontWeight", "borderBottom", "background"] as const) {
      expect(button.style[prop], `${prop} differs`).toBe(link.style[prop]);
    }
    expect(button.style.borderBottom).toContain("3px");
  });

  it("shows the err count as a pill and a plain count as a muted suffix", () => {
    wrap(
      <TabStrip
        ariaLabel="x"
        items={[
          { key: "e", label: "Errors", to: "/e", active: false, count: 4200, tone: "err", countTitle: "4200 open" },
          { key: "m", label: "Messages", to: "/m", active: false, count: 12400 },
        ]}
      />,
    );
    // Formatted, not raw: a five-digit count would widen the strip.
    expect(screen.getByTitle("4200 open").textContent).toBe("4.2k");
    expect(screen.getByRole("link", { name: /Messages/ }).textContent).toContain("· 12.4k");
  });

  it("omits the pill when an err tab has no open issues", () => {
    wrap(
      <TabStrip
        ariaLabel="x"
        items={[{ key: "e", label: "Errors", to: "/e", active: false, count: 0, tone: "err" }]}
      />,
    );
    expect(screen.getByRole("link", { name: "Errors" }).textContent).toBe("Errors");
  });

  it("renders a disabled tab as neither link nor button", () => {
    const onClick = vi.fn();
    wrap(
      <TabStrip
        ariaLabel="x"
        items={[{ key: "d", label: "Soon", active: false, disabled: true, onClick }]}
      />,
    );
    expect(screen.queryByRole("link", { name: "Soon" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Soon" })).toBeNull();
    expect(onClick).not.toHaveBeenCalled();
  });
});
