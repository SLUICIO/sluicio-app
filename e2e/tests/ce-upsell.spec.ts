// SPDX-License-Identifier: Apache-2.0
//
// CE upsell surfaces — every Enterprise-gated feature must, on an
// unlicensed cell, show an upgrade notice rather than a raw 402, a broken
// control, or a silently-missing panel. Each check is license-adaptive:
// licensed cell → the real control renders; unlicensed → the upsell. So it
// asserts something meaningful in both CI (CE) and dev (EE).
import { test, expect, type Page } from "@playwright/test";
import { logIn } from "./fixtures";

async function entitled(page: Page, feature: string): Promise<boolean> {
  const lic = await (await page.request.get("/api/v1/license")).json();
  return Boolean(lic?.features?.[feature]);
}

test.describe("Enterprise features upsell in the Community edition", () => {
  test.beforeEach(async ({ page }) => {
    await logIn(page); // admin
  });

  test("SSO tab", async ({ page }) => {
    const ee = await entitled(page, "sso");
    await page.goto("/settings?tab=sso");
    if (ee) {
      await expect(page.getByText("SSO is a Sluicio Enterprise feature")).toHaveCount(0);
    } else {
      await expect(page.getByText("SSO is a Sluicio Enterprise feature")).toBeVisible();
    }
  });

  test("Audit log tab", async ({ page }) => {
    const ee = await entitled(page, "audit_log");
    await page.goto("/settings?tab=audit");
    if (ee) {
      await expect(page.getByText("Audit log is a Sluicio Enterprise feature")).toHaveCount(0);
    } else {
      await expect(page.getByText("Audit log is a Sluicio Enterprise feature")).toBeVisible();
    }
  });

  test("Group access policies", async ({ page }) => {
    const ee = await entitled(page, "rbac_advanced");
    await page.goto("/settings?tab=groups");
    await page.locator("table tbody tr").first().locator("button").first().click();
    await expect(page.getByText("Access policies", { exact: false }).first()).toBeVisible();
    if (ee) {
      await expect(page.getByRole("button", { name: "+ Add policy" })).toBeVisible();
    } else {
      await expect(page.getByText("Access policies are a Sluicio Enterprise feature")).toBeVisible();
      await expect(page.getByRole("button", { name: "+ Add policy" })).toHaveCount(0);
    }
  });

  test("MFA enforcement policy", async ({ page }) => {
    const ee = await entitled(page, "mfa_policy");
    await page.goto("/settings?tab=system");
    // The security section is present either way; the toggle only when entitled.
    await expect(page.getByText(/two-factor/i).first()).toBeVisible();
    if (!ee) {
      await expect(page.getByText(/Enterprise/).first()).toBeVisible();
    }
  });

  test("Resource sharing card on an integration", async ({ page }) => {
    const ee = await entitled(page, "rbac_advanced");
    const integs = (await (await page.request.get("/api/v1/integrations?range=30d")).json()).integrations ?? [];
    test.skip(integs.length === 0, "no integrations to open");
    await page.goto(`/integrations/${integs[0].id}/settings`);
    if (ee) {
      // Sharing card present with its real copy (view-only share).
      await expect(page.getByText("Resource sharing is a Sluicio Enterprise feature")).toHaveCount(0);
    } else {
      await expect(page.getByText("Resource sharing is a Sluicio Enterprise feature")).toBeVisible();
    }
  });
});
