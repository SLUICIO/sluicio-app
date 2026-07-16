// SPDX-License-Identifier: Apache-2.0
//
// Message-search FilterEditor UI (2026-07-16 findings review): the
// error-type value picker offers the OBSERVED error types (not a blind
// text box), and integration↔service cross-narrowing — once one side
// is chosen, the other picker only offers compatible values.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

test("error-type list + integration↔service cross-narrowing on /search", async ({ page }) => {
  test.setTimeout(90_000);
  await logIn(page);
  const admin = page.request;
  const stamp = Date.now().toString(36);
  // Two integrations with disjoint members.
  const mkA = await admin.post("/api/v1/integrations", {
    data: { slug: `probe-a-${stamp}`, name: `Probe A ${stamp}`, matchers: [{ operator: "equals", value: "order-api" }] },
  });
  const a = (await mkA.json()).integration.id;
  const mkB = await admin.post("/api/v1/integrations", {
    data: { slug: `probe-b-${stamp}`, name: `Probe B ${stamp}`, matchers: [{ operator: "equals", value: "payment-service" }] },
  });
  const b = (await mkB.json()).integration.id;

  try {
    // ?s seeds a private DRAFT view — deterministic row set regardless
    // of the org's shared saved views (which other suite workers touch;
    // starting from the default shared view made pill indexing racy).
    await page.goto("/search?s=any");
    await expect(page.getByRole("button", { name: "+ add a filter" })).toBeVisible();
    // Add a filter row → defaults to payload; switch it to error type.
    await page.getByRole("button", { name: "+ add a filter" }).click();
    await page.getByRole("button", { name: /payload/ }).last().click();
    await page.getByRole("button", { name: "error type", exact: true }).click();
    // Open the value pill → the observed error-type list must render.
    await page.getByRole("button", { name: /—/ }).last().click();
    await expect(page.getByText("Error types seen in this window")).toBeVisible();
    await page.keyboard.press("Escape");

    // Switch the row to integration and pick Probe A.
    await page.getByRole("button", { name: /error type ▾|error type/ }).first().click();
    await page.getByRole("button", { name: "integration", exact: true }).click();
    await page.getByRole("button", { name: /—/ }).last().click();
    await page.getByRole("button", { name: `Probe A ${stamp}` }).click();

    // Add a service row: the picker must offer ONLY Probe A's member.
    await page.getByRole("button", { name: "+ add a filter" }).click();
    await page.getByRole("button", { name: /payload/ }).last().click();
    await page.getByRole("button", { name: "service", exact: true }).click();
    await page.getByRole("button", { name: /—/ }).last().click();
    await expect(page.getByRole("button", { name: "order-api" })).toBeVisible();
    await expect(page.getByRole("button", { name: "payment-service" })).toHaveCount(0);
    await page.getByRole("button", { name: "order-api" }).click();

    // Flip the check: with service=order-api chosen, the integration
    // picker offers Probe A but not Probe B.
    await page.getByRole("button", { name: `Probe A ${stamp}` }).click();
    await expect(page.getByRole("button", { name: `Probe A ${stamp}` }).last()).toBeVisible();
    await expect(page.getByRole("button", { name: `Probe B ${stamp}` })).toHaveCount(0);
  } finally {
    await admin.delete(`/api/v1/integrations/${a}`);
    await admin.delete(`/api/v1/integrations/${b}`);
  }
});
