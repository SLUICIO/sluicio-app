// SPDX-License-Identifier: Apache-2.0
//
// Monitoring templates page: built-ins list their checks before you fork
// (no more forking blind), and custom templates can be edited in place —
// tune a threshold/severity, remove a check, save. Also pins the sidebar
// footer's GitHub link (the in-product pointer to the project).
import { test, expect, request as pwRequest } from "@playwright/test";
import { logIn, ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";

test.describe("Monitoring templates", () => {
  test("built-ins show their checks before forking", async ({ page }) => {
    await logIn(page);
    await page.goto("/monitoring-templates");
    const builtins = page.locator(".card", { hasText: "Built-in templates" });
    await expect(builtins.getByText("RabbitMQ")).toBeVisible();
    // Expand → the check lines (name + condensed condition) appear.
    await builtins.getByRole("button", { name: "RabbitMQ", exact: true }).click();
    await expect(builtins.getByText(/metric · |log · /).first()).toBeVisible();
  });

  test("edit a forked template's checks in place", async ({ page }) => {
    // Fork via API for speed; exercise the editor through the UI.
    const admin = await pwRequest.newContext({ baseURL: BASE_URL });
    await admin.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
    const name = `e2e tmpl ${Date.now()}`;
    const created = await (
      await admin.post("/api/v1/monitoring-templates", { data: { name, fork_kind: "rabbitmq" } })
    ).json();
    try {
      await logIn(page);
      await page.goto("/monitoring-templates");
      await page.locator(".card", { hasText: "Your templates" }).getByRole("button", { name, exact: true }).waitFor();

      // The innermost div holding both the template name and its actions
      // is the row header.
      const row = page
        .locator("div")
        .filter({ has: page.getByRole("button", { name, exact: true }) })
        .filter({ has: page.getByRole("button", { name: "Edit checks" }) })
        .last();
      await row.getByRole("button", { name: "Edit checks" }).click();

      // Rename the first check + escalate it to critical.
      await page.getByLabel("Check name").first().fill("e2e tuned check");
      await page.getByLabel("Severity").first().selectOption("critical");
      await page.getByRole("button", { name: "Save checks" }).click();

      // Persisted and re-rendered in the (still expanded) read view.
      await expect(page.getByText("e2e tuned check")).toBeVisible();
      await expect(page.getByText("CRITICAL").first()).toBeVisible();
    } finally {
      await admin.delete(`/api/v1/monitoring-templates/${created.id}`);
      await admin.dispose();
    }
  });
});

test.describe("Sidebar footer", () => {
  test("version links to the GitHub releases", async ({ page }) => {
    await logIn(page);
    const link = page.locator("aside").getByRole("link", { name: /^v.+·/ });
    await expect(link).toHaveAttribute("href", "https://github.com/SLUICIO/sluicio-app/releases");
    await expect(link).toHaveAttribute("target", "_blank");
  });
});
