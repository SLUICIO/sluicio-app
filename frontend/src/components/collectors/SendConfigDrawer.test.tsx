// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({
  validateCollectorConfig: vi.fn(),
  sendCollectorConfig: vi.fn(),
}));
vi.mock("../../api/client", () => ({ api }));
vi.mock("../CodeEditor", () => ({ default: () => null }));
vi.mock("../../lib/useLicense", () => ({ useFeature: () => false }));

const { default: SendConfigDrawer } = await import("./SendConfigDrawer");

describe("the send drawer's review", () => {
  it("warns that a setting from the local files stays in effect", async () => {
    api.validateCollectorConfig.mockResolvedValue({
      checked: true,
      validation: { valid: true, diagnostics: [] },
      kept_by_local: ["receivers.host_metrics.scrapers.cpu"],
    });
    render(
      <MemoryRouter>
        <SendConfigDrawer
          collectorId="c1"
          collectorName="gw-drift"
          current={
            "receivers:\n  host_metrics:\n    scrapers:\n      cpu: {}\n"
          }
          initial={"receivers:\n  host_metrics:\n    scrapers: {}\n"}
          requireApproval={false}
          onClose={() => undefined}
          onSent={() => undefined}
        />
      </MemoryRouter>,
    );
    const warning = await screen.findByText(
      /comes from the collector's local configuration files/,
    );
    expect(warning.textContent).toMatch(
      /receivers\.host_metrics\.scrapers\.cpu\. Remove it from those files on the host\./,
    );
  });
});
