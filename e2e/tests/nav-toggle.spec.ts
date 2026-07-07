// SPDX-License-Identifier: Apache-2.0
//
// Sidebar show/hide toggle — the hamburger in the top bar hides the
// side nav (useful until the product is properly mobile-friendly) and
// the preference persists across reloads via localStorage.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

test.describe("Sidebar visibility toggle", () => {
  test("hide, persist across reload, show again", async ({ page }) => {
    await logIn(page);
    const nav = page.getByRole("link", { name: "Integrations" });
    await expect(nav).toBeVisible();

    await page.getByRole("button", { name: "Hide navigation" }).click();
    await expect(nav).toHaveCount(0);

    // Sticky: still hidden after a full reload.
    await page.reload();
    await expect(page.getByRole("button", { name: "Show navigation" })).toBeVisible();
    await expect(page.getByRole("link", { name: "Integrations" })).toHaveCount(0);

    await page.getByRole("button", { name: "Show navigation" }).click();
    await expect(page.getByRole("link", { name: "Integrations" })).toBeVisible();
  });
});
