// SPDX-License-Identifier: Apache-2.0
//
// Multi-level grouping on the integrations list: group by one metadata
// field (country), then a second (business unit). Group headers carry
// integration counts + aggregated traffic; the ?group= URL is shareable.
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { logIn, ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";

test.describe("Integrations grouped by metadata", () => {
  let admin: APIRequestContext;
  const fieldIDs: string[] = [];
  const integIDs: string[] = [];

  test.beforeAll(async () => {
    admin = await pwRequest.newContext({ baseURL: BASE_URL });
    await admin.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
    for (const [key, label] of [["e2e-country", "E2E Country"], ["e2e-bu", "E2E Business Unit"]] as const) {
      const f = await (
        await admin.post("/api/v1/metadata-fields", {
          data: {
            key, label, type: "text", description: "",
            applies_to_integration: true, applies_to_service: false, applies_to_system: false,
            system_type_key: "", required: false,
          },
        })
      ).json();
      fieldIDs.push(f.id);
    }
    const specs = [
      { slug: "e2e-grp-se-retail", country: "Sweden", bu: "Retail" },
      { slug: "e2e-grp-se-fin", country: "Sweden", bu: "Finance" },
      { slug: "e2e-grp-de-retail", country: "Germany", bu: "Retail" },
    ];
    for (const sp of specs) {
      const created = await (
        await admin.post("/api/v1/integrations", {
          data: { slug: sp.slug, name: sp.slug, matchers: [{ operator: "equals", value: sp.slug }] },
        })
      ).json();
      const id = created.integration?.id ?? created.id;
      integIDs.push(id);
      await admin.put(`/api/v1/integrations/${id}/metadata`, {
        data: { "e2e-country": sp.country, "e2e-bu": sp.bu },
      });
    }
  });

  test.afterAll(async () => {
    for (const id of integIDs) await admin.delete(`/api/v1/integrations/${id}`);
    for (const id of fieldIDs) await admin.delete(`/api/v1/metadata-fields/${id}`);
    await admin.dispose();
  });

  test("two-level grouping renders nested headers with aggregates", async ({ page }) => {
    await logIn(page);
    await page.goto("/integrations?group=e2e-country,e2e-bu");

    const headers = page.locator(".integration-group-row");
    // Sweden + Germany (level 1) and Retail/Finance under Sweden +
    // Retail under Germany (level 2) — at least 5 headers.
    await expect(headers.nth(4)).toBeVisible();

    const sweden = headers.filter({ hasText: "Sweden" }).first();
    await expect(sweden).toContainText("E2E Country:");
    await expect(sweden).toContainText("2 integrations");
    await expect(headers.filter({ hasText: "Germany" }).first()).toContainText("1 integration");
    // Business-unit level nests under countries.
    await expect(
      headers.filter({ hasText: "E2E Business Unit" }).filter({ hasText: "Retail" }).first(),
    ).toBeVisible();
    // Every fixture row is present under its groups.
    await expect(page.getByRole("link", { name: "e2e-grp-se-fin" })).toBeVisible();
  });

  test("the group-by controls drive the URL", async ({ page }) => {
    await logIn(page);
    await page.goto("/integrations");
    await page.getByRole("button", { name: "No grouping" }).click();
    await page.getByRole("option", { name: "E2E Country" }).click();
    await expect(page).toHaveURL(/group=e2e-country/);
    await expect(page.locator(".integration-group-row").first()).toBeVisible();
  });
});
