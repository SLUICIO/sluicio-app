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
vi.mock("../../lib/useLicense", () => ({ useFeature: () => true }));

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
    // Only the collector the change applies to can be ticked; nothing is
    // stamped, so nothing is ticked to begin with.
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes[0]).not.toBeChecked();
    expect(boxes[1]).toBeDisabled();
    fireEvent.click(boxes[0]);
    fireEvent.click(screen.getByRole("button", { name: "Review the change" }));
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
    fireEvent.click((await screen.findAllByRole("checkbox"))[0]);
    fireEvent.click(screen.getByRole("button", { name: "Review the change" }));
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
    fireEvent.click((await screen.findAllByRole("checkbox"))[0]);
    fireEvent.click(screen.getByRole("button", { name: "Review the change" }));
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

  it("proposes one change per collector, the stamped carriers ticked first", async () => {
    vi.clearAllMocks();
    api.listAdvisorSuggestionCollectors.mockResolvedValue({
      collectors: [
        {
          collector: collector("c1", "gw-1"),
          changed: ["metrics"],
          skipped: [],
          already_dropped: false,
          stamped: true,
          carried: 900,
        },
        {
          collector: { ...collector("c3", "gw-3"), group_name: "Payments" },
          changed: ["metrics"],
          skipped: [],
          already_dropped: false,
          stamped: true,
          carried: 40,
        },
        {
          collector: collector("c4", "gw-4"),
          changed: ["metrics"],
          skipped: [],
          already_dropped: false,
          stamped: true,
          carried: 0,
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
      validation: { valid: true, diagnostics: [] },
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
              scope_id: "m",
              title: "Nothing reads m",
            } as never
          }
          onClose={() => undefined}
        />
      </MemoryRouter>,
    );
    expect(
      await screen.findByText("Carried 900 in the last 24 hours."),
    ).toBeInTheDocument();
    const boxes = screen.getAllByRole("checkbox");
    expect(boxes.map((b) => (b as HTMLInputElement).checked)).toEqual([
      true,
      true,
      false,
    ]);

    fireEvent.click(screen.getByRole("button", { name: "Review 2 changes" }));
    expect(
      await screen.findByRole("button", { name: "Propose 2 changes" }),
    ).toBeInTheDocument();
    expect(api.validateCollectorConfig).toHaveBeenCalledTimes(2);

    fireEvent.click(screen.getByRole("button", { name: "Propose 2 changes" }));
    expect(
      await screen.findByText("Proposed to Payments."),
    ).toBeInTheDocument();
    expect(api.sendCollectorConfig).toHaveBeenCalledTimes(2);
    expect(api.sendCollectorConfig).toHaveBeenCalledWith(
      "c3",
      expect.objectContaining({ suggestion_id: "s1", config: after }),
    );
    expect(
      screen.getByText(/approval by the team that owns its collector/),
    ).toBeInTheDocument();
  });

  it("offers a pipeline-owned collector the change as a diff, never a proposal", async () => {
    vi.clearAllMocks();
    api.listAdvisorSuggestionCollectors.mockResolvedValue({
      collectors: [
        {
          collector: { ...collector("c5", "gw-5"), config_owner: "pipeline" },
          changed: ["metrics"],
          skipped: [],
          already_dropped: false,
          stamped: true,
          carried: 500,
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

    render(
      <MemoryRouter>
        <AdvisorCollectorDrawer
          suggestion={
            {
              id: "s1",
              class: "T1",
              scope_id: "m",
              title: "Nothing reads m",
            } as never
          }
          onClose={() => undefined}
        />
      </MemoryRouter>,
    );
    const box = await screen.findByRole("checkbox");
    expect(box).toBeDisabled();
    expect(box).not.toBeChecked();
    fireEvent.click(screen.getByRole("button", { name: "Get the diff" }));
    expect(
      await screen.findByText(/\+\s+filter\/sluicio-advisor:/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Download .patch" }),
    ).toBeInTheDocument();
    expect(api.sendCollectorConfig).not.toHaveBeenCalled();
  });
});
