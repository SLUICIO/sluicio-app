// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A check that finds no data cannot fire, so the list says so instead of
// letting it pass for a healthy one.

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import NoDataBadge from "./NoDataBadge";

describe("NoDataBadge", () => {
  it("marks a check whose last evaluation found nothing", () => {
    render(<NoDataBadge state={{ state: "no_data", firing_instances: 0, last_evaluated_at: new Date().toISOString() }} />);
    expect(screen.getByText("no data").getAttribute("title")).toMatch(/cannot fire/);
  });

  it("stays out of the way otherwise", () => {
    const { container } = render(
      <>
        <NoDataBadge state={{ state: "ok", firing_instances: 0 }} />
        <NoDataBadge state={{ state: "disabled", firing_instances: 0 }} />
        <NoDataBadge />
      </>,
    );
    expect(container.textContent).toBe("");
  });
});
