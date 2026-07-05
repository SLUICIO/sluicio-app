// SPDX-License-Identifier: Apache-2.0
//
// Integration + metadata lifecycle — a full create → search → annotate →
// search-by-metadata → delete flow, driven through the real UI. Proves the
// integrations list filter (by name and by a metadata-field value) reflects
// what was created and edited, and that creates/edits/deletes round-trip.
//
// The list filter is URL-encoded as ?filter=field|op|value (pipe-delimited; the
// metadata field is meta:<key>). The filter is applied client-side over the
// loaded list, so navigating with the filter is the same code path the filter
// builder drives — it just skips the click-by-click building.
import { test, expect, type Page, type APIRequestContext } from "@playwright/test";
import { ADMIN_EMAIL, ADMIN_PASSWORD, logIn } from "./fixtures";

const INT_A = "Playwright-Order-Int";
const INT_B = "Random-order-INT";
const FIELD_LABEL = "Playwright-verifier";
const FIELD_KEY = "playwright_verifier";
const META_VALUE = "1 2 3 a b petrrr";

// Best-effort API cleanup so the spec is re-runnable with these exact names
// (integration slugs are unique — a leftover from a failed run would 409 the
// create). Uses the request fixture, which goes through the same dev-server
// proxy the page does; we just need our own login for its cookie jar.
async function apiCleanup(request: APIRequestContext): Promise<void> {
  const login = await request.post("/api/v1/auth/login", {
    data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD },
  });
  if (!login.ok()) return;
  const ints = await request.get("/api/v1/integrations");
  if (ints.ok()) {
    for (const it of (await ints.json()).integrations ?? []) {
      if (it.name === INT_A || it.name === INT_B) {
        await request.delete(`/api/v1/integrations/${it.id}`);
      }
    }
  }
  const fields = await request.get("/api/v1/metadata-fields");
  if (fields.ok()) {
    for (const f of (await fields.json()).fields ?? []) {
      if (f.key === FIELD_KEY || f.label === FIELD_LABEL) {
        await request.delete(`/api/v1/metadata-fields/${f.id}`);
      }
    }
  }
}

async function createIntegration(page: Page, name: string): Promise<string> {
  await page.goto("/integrations/new");
  await page.getByLabel("Name").fill(name); // slug auto-fills from the name
  await page.getByRole("button", { name: "Create integration" }).click();
  await page.waitForURL(/\/integrations\/[0-9a-f-]{36}/);
  return new URL(page.url()).pathname.split("/").filter(Boolean).pop() as string;
}

test.describe("Integration + metadata lifecycle", () => {
  test("create, search by name + metadata, then delete", async ({ page, request }) => {
    test.setTimeout(90_000);
    await apiCleanup(request);
    page.on("dialog", (d) => d.accept()); // auto-confirm the delete prompts
    await logIn(page);

    // 1–2. Create both integrations.
    const idA = await createIntegration(page, INT_A);
    const idB = await createIntegration(page, INT_B);
    expect(idA).not.toEqual(idB);

    // 3. Search "Playwright" → only INT_A shows. (Row name is a link; the
    // lowercase slug also renders, so target the link to stay unambiguous.)
    await page.goto("/integrations?filter=name|contains|Playwright");
    await expect(page.getByRole("link", { name: INT_A })).toBeVisible();
    await expect(page.getByRole("link", { name: INT_B })).toHaveCount(0);

    // 4. Search "Random" → INT_B shows.
    await page.goto("/integrations?filter=name|contains|Random");
    await expect(page.getByRole("link", { name: INT_B })).toBeVisible();

    // 5. Define a text metadata field (defaults: type=text, applies to integrations).
    await page.goto("/metadata-fields");
    await page.getByRole("button", { name: /New field/ }).click();
    await page.getByLabel("Label").fill(FIELD_LABEL);
    await page.getByLabel("Key").fill(FIELD_KEY); // explicit key for the meta: filter
    await page.getByRole("button", { name: "Create field" }).click();
    await expect(page.getByRole("row", { name: new RegExp(FIELD_LABEL) })).toBeVisible();

    // 6. Set the value on INT_A. The org may have its own *required* metadata
    // fields (the save is blocked until they're set), so fill every text field
    // in the panel first, then set ours specifically.
    await page.goto(`/integrations/${idA}/metadata`);
    await page.getByRole("button", { name: /Edit/ }).click();
    const panel = page.locator("section").filter({
      has: page.getByRole("heading", { name: "Metadata", level: 2 }),
    });
    const boxes = panel.getByRole("textbox");
    for (let i = 0, n = await boxes.count(); i < n; i++) {
      await boxes.nth(i).fill("e2e");
    }
    await page.getByLabel(FIELD_LABEL).fill(META_VALUE);
    await page.getByRole("button", { name: "Save" }).click();
    // Reload the tab (deterministic — the in-place refresh isn't awaited) and
    // confirm the value persisted in view mode.
    await page.goto(`/integrations/${idA}/metadata`);
    await expect(page.getByText(META_VALUE)).toBeVisible();

    // 7. Search the metadata value "petr" → INT_A shows.
    await page.goto(`/integrations?filter=meta:${FIELD_KEY}|contains|petr`);
    await expect(page.getByRole("link", { name: INT_A })).toBeVisible();

    // 8. Delete INT_B.
    await page.goto(`/integrations/${idB}`);
    await page.getByRole("button", { name: "Delete" }).click();
    await page.waitForURL(/\/integrations$/);

    // 9. Delete INT_A.
    await page.goto(`/integrations/${idA}`);
    await page.getByRole("button", { name: "Delete" }).click();
    await page.waitForURL(/\/integrations$/);

    // 10. Delete the metadata field.
    await page.goto("/metadata-fields");
    await page.getByRole("row", { name: new RegExp(FIELD_LABEL) }).getByRole("button", { name: "Delete" }).click();
    await expect(page.getByText(FIELD_LABEL)).toHaveCount(0);
  });
});
