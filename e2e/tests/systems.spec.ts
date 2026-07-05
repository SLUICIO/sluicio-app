// SPDX-License-Identifier: Apache-2.0
//
// Systems coverage — the system-types catalog, the systems entity list, and the
// dashboard "systems running" KPI (docs/systems.md phases 1–4). Read-only
// assertions: they prove the routes render and are wired to the API without
// mutating org state. A full create → attach → delete flow can be layered on.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

test.describe("Systems", () => {
  // Phase 1: the system-types catalog lists the code-seeded built-ins.
  test("system-types catalog lists built-in types", async ({ page }) => {
    await logIn(page);
    await page.goto("/system-types");
    await expect(page.getByRole("heading", { name: "System types" })).toBeVisible();
    // Built-ins are always present (seeded in code, not the DB).
    await expect(page.getByText("RabbitMQ").first()).toBeVisible();
    await expect(page.getByText("OpenTelemetry Collector").first()).toBeVisible();
  });

  // Phase 2: the Systems page renders the entity list (not a crash / login).
  test("systems page renders", async ({ page }) => {
    await logIn(page);
    await page.goto("/systems");
    await expect(page.getByRole("heading", { name: "Systems" })).toBeVisible();
    await expect(page.getByText("Sign in to Sluicio")).toBeHidden();
  });

  // Phase 4: the dashboard shows the systems-health KPI alongside integrations.
  test("dashboard shows a systems-health KPI", async ({ page }) => {
    await logIn(page);
    await page.goto("/health");
    await expect(page.getByText(/systems (running|unhealthy)/i)).toBeVisible();
  });
});
