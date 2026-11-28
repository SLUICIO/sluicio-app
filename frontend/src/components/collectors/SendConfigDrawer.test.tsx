// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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

  it("warns about a secret written in plain text", async () => {
    api.validateCollectorConfig.mockResolvedValue({
      checked: true,
      validation: { valid: true, diagnostics: [] },
      kept_by_local: [],
      plain_secrets: ["exporters.otlp_http/b.headers.x-api-key"],
    });
    render(
      <MemoryRouter>
        <SendConfigDrawer
          collectorId="c1"
          collectorName="gw"
          current={"exporters: {}\n"}
          initial={
            "exporters:\n  otlp_http/b:\n    headers:\n      x-api-key: abc\n"
          }
          requireApproval={false}
          onClose={() => undefined}
          onSent={() => undefined}
        />
      </MemoryRouter>,
    );
    const warning = await screen.findByText(
      /This configuration has a secret written in plain text/,
    );
    expect(warning.textContent).toMatch(
      /exporters\.otlp_http\/b\.headers\.x-api-key\..*\$\{env:NAME\}/,
    );
  });

  it("sends unchecked when OTelFlow cannot be reached", async () => {
    api.validateCollectorConfig.mockRejectedValue(
      Object.assign(
        new Error("502 OTelFlow could not check the configuration"),
        {
          status: 502,
          body: {
            checked: false,
            kept_by_local: [],
            plain_secrets: [],
            error: {
              message: "OTelFlow could not check the configuration: timeout",
              status: 502,
            },
          },
        },
      ),
    );
    api.sendCollectorConfig.mockResolvedValue({
      deployment: { id: "d1", status: "pending" },
      delivered: true,
    });
    render(
      <MemoryRouter>
        <SendConfigDrawer
          collectorId="c1"
          collectorName="gw"
          current={"receivers: {}\n"}
          initial={"receivers:\n  otlp: {}\n"}
          requireApproval={false}
          onClose={() => undefined}
          onSent={() => undefined}
        />
      </MemoryRouter>,
    );
    await screen.findByText(/timeout\. The change was not checked here/);
    fireEvent.click(screen.getByRole("button", { name: "Send unchecked" }));
    await waitFor(() =>
      expect(api.sendCollectorConfig).toHaveBeenCalledWith(
        "c1",
        expect.objectContaining({ force: true }),
      ),
    );
  });
});
