// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A source's metric names were one comma-separated text field rebuilt
// from the list on every keystroke, so the comma vanished as it was typed
// and a second name could not be entered. They are chips beside a picker
// of the names the origin has sent.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

vi.mock("../api/client", () => ({
  api: {
    listServiceMetricNames: (svc: string) =>
      Promise.resolve({
        metrics:
          svc === "rabbitmq-prod"
            ? [{ name: "rabbitmq.queue.messages" }, { name: "rabbitmq.queue.consumers" }]
            : [{ name: "http.server.duration" }],
      }),
    listMetricCatalog: () => Promise.resolve({ metrics: [{ name: "anything.at.all" }] }),
  },
}));

const { MetricNamesPicker } = await import("./IntegrationSources");

describe("a source's metric names", () => {
  it("suggests the origin's metrics and keeps each pick as a chip", async () => {
    const onChange = vi.fn();
    render(<MetricNamesPicker value={["rabbitmq.queue.messages"]} onChange={onChange} originKind="service" origin="rabbitmq-prod" systems={[]} />);
    expect(screen.getByRole("button", { name: "Remove rabbitmq.queue.messages" })).toBeTruthy();

    await userEvent.click(screen.getByRole("button", { name: /Add a metric/ }));
    const options = (await screen.findAllByRole("option")).map((o) => o.textContent);
    // Already kept names are not offered again; other services' are not offered at all.
    expect(options).toEqual(["rabbitmq.queue.consumers"]);
    await userEvent.click(screen.getByRole("option", { name: "rabbitmq.queue.consumers" }));
    expect(onChange).toHaveBeenLastCalledWith(["rabbitmq.queue.messages", "rabbitmq.queue.consumers"]);
  });

  it("takes a typed glob as it is", async () => {
    const onChange = vi.fn();
    render(<MetricNamesPicker value={[]} onChange={onChange} originKind="service" origin="rabbitmq-prod" systems={[]} />);
    expect(screen.getByText("all")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: /Add a metric/ }));
    await userEvent.type(screen.getByPlaceholderText(/type a name/), "rabbitmq.queue.*{Enter}");
    expect(onChange).toHaveBeenLastCalledWith(["rabbitmq.queue.*"]);
  });

  it("removes a chip", async () => {
    const onChange = vi.fn();
    render(<MetricNamesPicker value={["a", "b"]} onChange={onChange} originKind="service" origin="svc" systems={[]} />);
    await userEvent.click(screen.getByRole("button", { name: "Remove a" }));
    expect(onChange).toHaveBeenLastCalledWith(["b"]);
  });
});
