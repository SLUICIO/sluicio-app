// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const state = vi.hoisted(() => ({ admin: false, limits: {} as Record<string, unknown> }));
vi.mock("../lib/useCurrentUser", () => ({ useCurrentUser: () => ({ can: () => state.admin }) }));
vi.mock("../api/client", () => ({ api: { getLimits: () => Promise.resolve(state.limits) } }));

const { DeploymentLimitsBanner } = await import("./DeploymentLimitsBanner");

describe("the deployment limits banner", () => {
  it("tells everyone when telemetry is being refused", async () => {
    state.admin = false;
    state.limits = { storage: { limit_bytes: 50e9, used_bytes: 50.2e9, at_limit: true } };
    render(<DeploymentLimitsBanner />);
    expect((await screen.findByRole("alert")).textContent).toMatch(/Storage limit reached.*50 GB of its 50 GB.*refused/);
  });

  it("warns admins, and only admins, from 80%", async () => {
    state.limits = { storage: { limit_bytes: 50e9, used_bytes: 42e9 } };
    state.admin = true;
    const { unmount } = render(<DeploymentLimitsBanner />);
    expect((await screen.findByRole("status")).textContent).toMatch(/Storage at 84%/);
    unmount();
    state.admin = false;
    render(<DeploymentLimitsBanner />);
    await new Promise((r) => setTimeout(r, 0));
    expect(screen.queryByRole("status")).toBeNull();
  });

  it("tells admins when no integration or system can be added", async () => {
    state.admin = true;
    state.limits = { monitored_entities: { used: 10, limit: 10 } };
    render(<DeploymentLimitsBanner />);
    expect((await screen.findByRole("status")).textContent).toMatch(/10 of 10 integrations and systems/);
  });

  it("shows nothing when the deployment sets no limits", async () => {
    state.admin = true;
    state.limits = {};
    const { container } = render(<DeploymentLimitsBanner />);
    await new Promise((r) => setTimeout(r, 0));
    expect(container.textContent).toBe("");
  });
});
