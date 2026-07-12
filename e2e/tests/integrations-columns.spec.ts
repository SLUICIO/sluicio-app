// SPDX-License-Identifier: Apache-2.0
//
// Integrations column layout: columns can be reordered (and hidden) via
// the Columns picker, and the layout persists PER USER on the server —
// a fresh navigation with no query string and no localStorage must
// restore it. ?cols= keeps overriding for shared links.
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { logIn, ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";
const PREF_KEY = "integrations.columns";

test.describe("Integrations column layout", () => {
  let admin: APIRequestContext;
  let integID: string;

  test.beforeAll(async () => {
    admin = await pwRequest.newContext({ baseURL: BASE_URL });
    await admin.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
    // Clean layout slate, then one integration so the table renders.
    await admin.put(`/api/v1/me/preferences/${PREF_KEY}`, { data: { value: null } });
    const created = await (
      await admin.post("/api/v1/integrations", {
        data: { slug: "e2e-cols-integ", name: "E2E Cols", matchers: [{ operator: "equals", value: "e2e-cols-svc" }] },
      })
    ).json();
    integID = created.integration?.id ?? created.id;
  });

  test.afterAll(async () => {
    await admin.delete(`/api/v1/integrations/${integID}`);
    await admin.put(`/api/v1/me/preferences/${PREF_KEY}`, { data: { value: null } });
    await admin.dispose();
  });

  test("reorder + hide persists per user across a clean navigation", async ({ page }) => {
    await logIn(page);
    await page.goto("/integrations");
    const ths = page.locator("table thead th");
    await expect(ths.first()).toContainText("Name");

    // Move Slug to the front (two steps up from position 3).
    await page.getByRole("button", { name: /Columns ·/ }).click();
    const menu = page.getByRole("menu");
    await menu.getByRole("button", { name: "Move Slug up" }).click();
    await menu.getByRole("button", { name: "Move Slug up" }).click();
    // Hide Description.
    await menu.locator("label", { hasText: "Description" }).locator("input").uncheck();
    await page.keyboard.press("Escape");

    await expect(ths.first()).toContainText("Slug");
    await expect(ths.filter({ hasText: "Description" })).toHaveCount(0);

    // Fresh navigation: no ?cols=, no localStorage — only the per-user
    // server preference can restore the layout.
    await page.evaluate(() => window.localStorage.removeItem("im.integrations.cols"));
    await page.goto("/integrations");
    await expect(ths.first()).toContainText("Slug");
    await expect(ths.filter({ hasText: "Description" })).toHaveCount(0);

    // Reset returns to defaults.
    await page.getByRole("button", { name: /Columns ·/ }).click();
    await page.getByRole("menu").getByRole("button", { name: "reset" }).click();
    await page.keyboard.press("Escape");
    await expect(ths.first()).toContainText("Name");
    await expect(ths.filter({ hasText: "Description" })).toHaveCount(1);
  });

  test("?cols= still pins a shared view over the stored preference", async ({ page }) => {
    await logIn(page);
    await page.goto("/integrations?cols=status,name");
    const ths = page.locator("table thead th");
    await expect(ths).toHaveCount(2);
    await expect(ths.nth(0)).toContainText("Status");
    await expect(ths.nth(1)).toContainText("Name");
  });
});
