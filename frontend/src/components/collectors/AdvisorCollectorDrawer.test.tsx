// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

const before =
  "processors:\n  batch: {}\nservice:\n  pipelines:\n    metrics:\n      processors: [batch]\n";
const after =
  "processors:\n  batch: {}\n  filter/sluicio-advisor:\n    error_mode: ignore\nservice:\n  pipelines:\n    metrics:\n      processors: [filter/sluicio-advisor, batch]\n";
const collector = (id: string, host: string) => ({
  id,
  group_id: "g",
  group_name: "Platform",
  instance_uid: id,
  kind: "supervisor",
  capabilities: [],
  description: { "host.name": host },
});
const api = vi.hoisted(() => ({
  listAdvisorSuggestionCollectors: vi.fn(),
  previewAdvisorCollectorChange: vi.fn(),
  validateCollectorConfig: vi.fn(),
  sendCollectorConfig: vi.fn(),
}));
vi.mock("../../api/client", () => ({ api }));
vi.mock("../CodeEditor", () => ({ default: () => null }));

const { default: AdvisorCollectorDrawer } =
  await import("./AdvisorCollectorDrawer");

describe("applying an Advisor suggestion to a collector", () => {
  it("offers only collectors the change applies to, and files it for the team's approval", async () => {
    api.listAdvisorSuggestionCollectors.mockResolvedValue({
      collectors: [
        {
          collector: collector("c1", "gw-1"),
          changed: ["metrics"],
          skipped: [
            {
              pipeline: "metrics/both",
              reason:
                "also sends to prometheus_remote_write, which would lose the metric too",
            },
          ],
          already_dropped: false,
        },
        {
          collector: collector("c2", "gw-2"),
          changed: [],
          skipped: [],
          already_dropped: false,
          unavailable: "none of its metrics pipelines sends only to Sluicio",
        },
      ],
    });
    api.previewAdvisorCollectorChange.mockResolvedValue({
      before,
      config: after,
      changed: ["metrics"],
      skipped: [],
      already_dropped: false,
    });
    api.validateCollectorConfig.mockResolvedValue({
      checked: true,
      validation: { valid: true, diagnostics: [], checked_version: "0.166.0" },
    });
    api.sendCollectorConfig.mockResolvedValue({
      deployment: { id: "d" },
      proposal: { id: "p" },
    });

    render(
      <MemoryRouter>
        <AdvisorCollectorDrawer
          suggestion={
            {
              id: "s1",
              class: "T1",
              scope_id: "system.cpu.load_average.15m",
            } as never
          }
          onClose={() => undefined}
        />
      </MemoryRouter>,
    );

    expect(await screen.findByText("gw-1")).toBeInTheDocument();
    expect(screen.getByText(/Left alone: metrics\/both/)).toBeInTheDocument();
    expect(
      screen.getByText(/Not here: none of its metrics pipelines/),
    ).toBeInTheDocument();
    const buttons = screen.getAllByRole("button", { name: "Review change" });
    expect(buttons).toHaveLength(1);

    fireEvent.click(buttons[0]);
    // Straight to the review: checked, and the change shown.
    expect(
      await screen.findByText(/OTelFlow found no errors/),
    ).toBeInTheDocument();
    expect(api.validateCollectorConfig).toHaveBeenCalledWith("c1", after);
    expect(
      screen.getAllByText(/filter\/sluicio-advisor/).length,
    ).toBeGreaterThan(0);

    fireEvent.click(
      screen.getByRole("button", { name: "Propose for approval" }),
    );
    expect(
      await screen.findByText(/waits for approval by the collector's team/),
    ).toBeInTheDocument();
    expect(api.sendCollectorConfig).toHaveBeenCalledWith("c1", {
      config: after,
      message: "",
      force: false,
      suggestion_id: "s1",
      suggestion_hash: false,
    });
  });

  it("deletes personal data by default and hashes it only when chosen", async () => {
    vi.clearAllMocks();
    api.listAdvisorSuggestionCollectors.mockResolvedValue({
      collectors: [
        {
          collector: collector("c1", "gw-1"),
          changed: ["traces"],
          skipped: [],
          already_dropped: false,
        },
      ],
    });
    api.previewAdvisorCollectorChange.mockResolvedValue({
      before,
      config: after,
      changed: ["traces"],
      skipped: [],
      already_dropped: false,
    });
    api.validateCollectorConfig.mockResolvedValue({
      checked: true,
      validation: { valid: true, diagnostics: [] },
    });
    api.sendCollectorConfig.mockResolvedValue({
      deployment: { id: "d" },
      proposal: { id: "p" },
    });
    const t6 = {
      id: "s6",
      class: "T6",
      scope_id: "orders-api.customer.email",
      title: "pii",
    } as never;

    const { unmount } = render(
      <MemoryRouter>
        <AdvisorCollectorDrawer suggestion={t6} onClose={() => undefined} />
      </MemoryRouter>,
    );
    const select = await screen.findByRole("combobox");
    expect(select).toHaveValue("delete");
    fireEvent.click(
      await screen.findByRole("button", { name: "Review change" }),
    );
    await screen.findByText(/OTelFlow found no errors/);
    expect(api.previewAdvisorCollectorChange).toHaveBeenLastCalledWith(
      "s6",
      "c1",
      false,
    );
    unmount();

    render(
      <MemoryRouter>
        <AdvisorCollectorDrawer suggestion={t6} onClose={() => undefined} />
      </MemoryRouter>,
    );
    fireEvent.change(await screen.findByRole("combobox"), {
      target: { value: "hash" },
    });
    expect(
      screen.getByText(/pseudonymisation, not anonymisation/),
    ).toBeInTheDocument();
    fireEvent.click(
      await screen.findByRole("button", { name: "Review change" }),
    );
    await screen.findByText(/OTelFlow found no errors/);
    expect(api.previewAdvisorCollectorChange).toHaveBeenLastCalledWith(
      "s6",
      "c1",
      true,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Propose for approval" }),
    );
    await screen.findByText(/waits for approval by the collector's team/);
    expect(api.sendCollectorConfig).toHaveBeenLastCalledWith(
      "c1",
      expect.objectContaining({ suggestion_id: "s6", suggestion_hash: true }),
    );
  });
});
