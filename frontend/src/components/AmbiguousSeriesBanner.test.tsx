// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The logic is pinned in lib/ambiguousSeries.test.ts. These pin that the
// component actually renders it — the failure mode being a banner that
// is correct and invisible because a prop was never plumbed through.

import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import AmbiguousSeriesBanner from "./AmbiguousSeriesBanner";

describe("AmbiguousSeriesBanner", () => {
  it("renders the warning, the count and the way out", () => {
    const { container } = render(<AmbiguousSeriesBanner aggregation="last" series={5} />);
    expect(screen.getByText("This reading is not well defined")).toBeInTheDocument();
    expect(container.textContent).toContain("5 separate series");
    expect(container.textContent).toMatch(/Narrow the filter/);
  });

  it("renders nothing at all when the reading is well defined", () => {
    // Not "renders an empty box": an empty bordered div under the
    // preview would read as a broken component on every healthy rule.
    const { container } = render(<AmbiguousSeriesBanner aggregation="max" series={5} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when the server sent no series count", () => {
    const { container } = render(<AmbiguousSeriesBanner aggregation="last" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("stands down when the rule splits by the attribute instead", () => {
    const { container } = render(
      <AmbiguousSeriesBanner aggregation="last" series={5} splitBy="http.status_class" />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
