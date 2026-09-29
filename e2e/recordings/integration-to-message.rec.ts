// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Walkthrough: build an integration from one service, let the traces
// suggest the next, then find one customer's order among its messages.
//
// Not a test - a recording. It drives the real UI at a pace a viewer
// can follow and leaves a video behind (see README.md). It runs against
// a seeded recording stack, never against a cell that holds real data:
// it creates an integration, and deletes the one it made last time.

import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { beat, caption, glideClick, glideTo, installCursor, requireEnv, typeInto } from "./stagecraft";

const NAME = "Order processing";
const SLUG = "order-processing";
const FIRST = "order-intake";
const SUGGESTED = "order-fulfillment";
const ATTRIBUTE = "customer.id";

async function signIn(page: Page): Promise<void> {
  // Through the API, so the video opens on the product rather than on a
  // password being typed.
  const res = await page.request.post("/api/v1/auth/login", {
    data: { email: requireEnv("RECORDING_EMAIL"), password: requireEnv("RECORDING_PASSWORD") },
  });
  expect(res.ok(), `sign-in failed: ${res.status()}`).toBeTruthy();
}

// Every take starts from the same place: the integration this recording
// builds must not exist yet.
async function clearPreviousTake(page: Page): Promise<void> {
  const res = await page.request.get("/api/v1/integrations");
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  const list: { id?: string; slug?: string; integration?: { id: string; slug: string } }[] =
    body.integrations ?? body ?? [];
  for (const row of list) {
    const it = row.integration ?? row;
    if (it.slug === SLUG && it.id) {
      const del = await page.request.delete(`/api/v1/integrations/${it.id}`);
      expect(del.ok(), `could not remove the previous take: ${del.status()}`).toBeTruthy();
    }
  }
}

// Ready means the stack lists both services and the traces link them.
// Checked before the browser opens, so a stack that needs more traffic
// fails with a reason instead of leaving a video of an empty suggestion
// strip - and no waiting lands in the recording.
test.beforeAll(async ({ playwright }) => {
  test.setTimeout(6 * 60_000);
  const api: APIRequestContext = await playwright.request.newContext({ baseURL: requireEnv("RECORDING_BASE_URL") });
  const login = await api.post("/api/v1/auth/login", {
    data: { email: requireEnv("RECORDING_EMAIL"), password: requireEnv("RECORDING_PASSWORD") },
  });
  expect(login.ok(), `sign-in failed: ${login.status()} - is the recording stack up?`).toBeTruthy();
  const ready = async () => {
    const svc = await (await api.get("/api/v1/services?range=24h")).json();
    const names = new Set((svc.services ?? []).map((s: { service_name: string }) => s.service_name));
    if (!names.has(FIRST) || !names.has(SUGGESTED)) return false;
    const nb = await (await api.get(`/api/v1/services/${FIRST}/neighbors?range=24h`)).json();
    return (nb.downstream ?? []).some((n: { service_name: string }) => n.service_name === SUGGESTED);
  };
  await expect
    .poll(ready, {
      message: `the stack does not list ${FIRST} -> ${SUGGESTED} yet; give the seeder a few more minutes`,
      timeout: 5 * 60_000,
      intervals: [5_000],
    })
    .toBe(true);
  await api.dispose();
});

test("an integration from one service, then one customer's order", async ({ page }) => {
  await installCursor(page);
  await signIn(page);
  await clearPreviousTake(page);
  // An empty "what's new" bell: a badge of 26 pulls the eye for the
  // whole take and has nothing to do with it.
  await page.request.post("/api/v1/digest/seen");

  // 1. Where integrations live.
  await page.goto("/integrations");
  await page.mouse.move(720, 420);
  await caption(page, "An integration is a flow across your services.");
  await beat(page, 2200);

  // 2. A new one, named for what it does.
  await glideClick(page, page.getByRole("link", { name: "New integration" }).or(page.getByRole("button", { name: "New integration" })).first());
  await expect(page).toHaveURL(/\/integrations\/new/);
  await caption(page, "Name it for what it does.");
  await typeInto(page, page.getByLabel(/^Name/).first(), NAME);
  await typeInto(page, page.getByLabel("Description"), "Orders from the file drop through to fulfilment.");

  // 3. The service where orders come in.
  await caption(page, "Add the service where orders come in.");
  const addBox = page.getByRole("combobox", { name: "Add a service or pattern" });
  await typeInto(page, addBox, FIRST);
  await beat(page, 600);
  await page.keyboard.press("Enter");
  const members = page.getByRole("list", { name: "Services" });
  await expect(members).toContainText(FIRST);
  // Its traffic, as soon as the preview has answered.
  await expect(members).toContainText(/msgs/, { timeout: 15_000 });
  await beat(page, 1500);

  // 4. The traces already know what comes next.
  await caption(page, "The traces show where orders go next. One click adds it.");
  const suggestion = page.getByRole("button", { name: new RegExp(`^Add ${SUGGESTED}`) });
  await expect(suggestion).toBeVisible({ timeout: 20_000 });
  await glideTo(page, suggestion);
  await beat(page, 900);
  await glideClick(page, suggestion);
  await expect(members).toContainText(SUGGESTED);
  await beat(page, 1200);

  // 5. What it adds up to, before anything is saved.
  await caption(page, "Before saving: exactly what this integration will contain.");
  await glideTo(page, page.getByRole("group", { name: "What this integration matches" }));
  await beat(page, 2800);

  // 6. Live at once.
  await caption(page, "");
  await glideClick(page, page.getByRole("button", { name: /Create integration/ }));
  await expect(page).toHaveURL(/\/integrations\/[0-9a-f-]{36}$/, { timeout: 15_000 });
  await caption(page, "Live at once: health, errors, and the flow across both services.");
  await beat(page, 3500);

  // 7. Its messages.
  await caption(page, "Every message is one trace, end to end.");
  await glideClick(page, page.getByRole("navigation", { name: "Integration sections" }).getByRole("link", { name: /^Messages/ }));
  await expect(page).toHaveURL(/\/messages/);
  await beat(page, 2000);

  // 8. One customer's orders, by a field inside the message.
  await caption(page, "Find one customer's orders by a field in the message itself.");
  await glideClick(page, page.getByRole("button", { name: "+ add a filter" }));
  // The new row's field pill: an attribute has not been chosen yet. The
  // pills' accessible names carry their dropdown arrow.
  await glideClick(page, page.getByRole("button", { name: "attribute ▾", exact: true }).last());
  await typeInto(page, page.getByPlaceholder(/^filter attributes…|^attribute key/).last(), "customer");
  await beat(page, 500);
  await glideClick(page, page.getByRole("button", { name: new RegExp(`^${ATTRIBUTE.replace(".", "\\.")}`) }).first());
  // The value pill reads "—" until a value is picked; the picker lists
  // the values the data actually carries.
  await glideClick(page, page.getByRole("button", { name: "— ▾", exact: true }).last());
  const values = page.getByPlaceholder("search values… (or type an exact value)");
  await expect(values).toBeVisible();
  const firstValue = values.locator("xpath=following::button[1]");
  await glideClick(page, firstValue);
  await page.keyboard.press("Escape");
  await beat(page, 2500);

  // 9. The whole path of that order.
  await caption(page, "The whole path of that order, and how long each step took.");
  const firstRow = page.locator('div[style*="position: absolute"][style*="display: grid"]').first();
  await glideClick(page, firstRow);
  await expect(page.getByRole("dialog", { name: "Trace detail" }).or(page.getByLabel("Trace detail"))).toBeVisible();
  await beat(page, 5000);

  await caption(page, "");
  await beat(page, 800);
});
