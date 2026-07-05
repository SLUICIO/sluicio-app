// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import KpiCard from "./KpiCard";

describe("KpiCard", () => {
  it("renders its label and value", () => {
    render(<KpiCard label="Requests" value={42} />);
    expect(screen.getByText("Requests")).toBeInTheDocument();
    expect(screen.getByText("42")).toBeInTheDocument();
  });

  it("renders the optional sub-row when provided", () => {
    render(<KpiCard label="Latency" value="120 ms" sub={<span>p95</span>} />);
    expect(screen.getByText("p95")).toBeInTheDocument();
  });

  // jsdom doesn't resolve CSS custom properties (var(--primary)) on
  // typed colour props, so we assert on the pixel/style values it does
  // keep — enough to prove each emphasis renders a distinct border.
  it("thickens the border to 2px for the selected emphasis", () => {
    const { container } = render(<KpiCard label="Active" value={1} emphasis="selected" />);
    const card = container.firstElementChild as HTMLElement;
    expect(card.style.borderWidth).toBe("2px");
  });

  it("uses a default 1px border with no left accent", () => {
    const { container } = render(<KpiCard label="Idle" value={0} />);
    const card = container.firstElementChild as HTMLElement;
    expect(card.style.borderWidth).toBe("1px");
    expect(card.style.borderLeft).toBe("");
  });

  it("draws an error left-accent for the attention emphasis", () => {
    const { container } = render(<KpiCard label="Down" value={3} emphasis="attention" />);
    const card = container.firstElementChild as HTMLElement;
    expect(card.style.borderLeft).toContain("var(--err)");
  });
});
