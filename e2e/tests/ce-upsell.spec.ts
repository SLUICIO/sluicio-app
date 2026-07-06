// SPDX-License-Identifier: Apache-2.0
//
// CE upsell surfaces — the UI must not offer Enterprise-gated writes on an
// unlicensed cell (clicking into a raw 402 is a bug, not a gate). This spec
// adapts to the cell it runs against: licensed → the real controls render;
// unlicensed → the upgrade notice renders instead. Either way it's a
// meaningful assertion, so it runs in both CI (CE) and dev (EE).
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

test.describe("Group policies respect the license state", () => {
  test("policy editor is offered iff rbac_advanced is entitled", async ({ page }) => {
    await logIn(page); // admin
    const lic = await (await page.request.get("/api/v1/license")).json();
    const entitled = Boolean(lic?.features?.rbac_advanced);

    await page.goto("/settings?tab=groups");
    // Open the first group's blade.
    const firstGroup = page.locator("table tbody tr").first().locator("button").first();
    await firstGroup.click();

    // The section header carries the Enterprise badge in both modes.
    await expect(page.getByText("Access policies", { exact: false }).first()).toBeVisible();

    if (entitled) {
      await expect(page.getByRole("button", { name: "+ Add policy" })).toBeVisible();
      await expect(page.getByText("Access policies are a Sluicio Enterprise feature")).toHaveCount(0);
    } else {
      await expect(page.getByText("Access policies are a Sluicio Enterprise feature")).toBeVisible();
      await expect(page.getByRole("button", { name: "+ Add policy" })).toHaveCount(0);
    }
  });
});
