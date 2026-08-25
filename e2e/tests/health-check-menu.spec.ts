// SPDX-License-Identifier: Apache-2.0
//
// The "+ Add health check" menu must be fully usable, on every scope it
// appears on.
//
// `.card` sets `overflow: hidden` so its rounded corners clip content.
// The menu was an absolutely-positioned child of the card header, so it
// was clipped at the card's bottom edge: the third option — failed
// traces, response time and low traffic, i.e. every trace-signal check —
// was sheared in half and could not be clicked. Two of the three check
// kinds looked like all of them.
//
// Visibility is not the assertion. The clipped item still reported a
// bounding box and still passed toBeVisible(); what it could not do was
// receive a click. So this hit-tests the CENTRE of each item and demands
// the element found there is the item itself — the same thing a user's
// cursor does, and the only check that distinguishes "rendered" from
// "reachable".
import { test, expect, type Locator, type Page } from "@playwright/test";
import { logIn } from "./fixtures";

/**
 * Whether a real cursor at the item's centre would reach the item.
 *
 * Compares ELEMENT IDENTITY, not text. A clipped menu is still in the
 * DOM, so elementFromPoint returns some ancestor whose textContent
 * happens to include the menu's own labels — an earlier version of this
 * test compared strings and passed against the very build it was written
 * to catch.
 */
async function isReachable(item: Locator): Promise<{ ok: boolean; blocker: string }> {
  return item.evaluate((el) => {
    const r = el.getBoundingClientRect();
    const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    return {
      ok: !!hit && (hit === el || el.contains(hit)),
      blocker: hit ? `${hit.nodeName.toLowerCase()}.${(hit as HTMLElement).className || "(no class)"}` : "nothing",
    };
  });
}

async function assertMenuFullyClickable(page: Page) {
  const btn = page.getByRole("button", { name: "+ Add health check" });
  await btn.scrollIntoViewIfNeeded();
  await btn.click();

  const items = page.getByRole("menuitem");
  await expect(items).toHaveCount(3);

  for (let i = 0; i < 3; i++) {
    const item = items.nth(i);
    const label = (await item.innerText()).trim();
    const { ok, blocker } = await isReachable(item);
    expect(ok, `"${label}" is not clickable — its centre belongs to ${blocker}`).toBe(true);
  }

  // The last option is the one that was lost; opening it proves the menu
  // is wired up and not merely painted in the right place.
  await items.nth(2).click();
  await expect(page.getByRole("button", { name: /Add health check$/ }).last()).toBeVisible();
}

test.describe("Add health check menu", () => {
  test("every option is clickable on an integration", async ({ page }) => {
    await logIn(page);
    const list = await (await page.request.get("/api/v1/integrations?range=30d")).json();
    const id = (list.integrations ?? [])[0]?.id as string | undefined;
    test.skip(!id, "cell has no integrations");
    // Health checks live on the settings page's Alerting tab. ?tab= is a
    // supported deep link, so this stays a navigation rather than a click
    // sequence that would break again on the next grouping change.
    await page.goto(`/integrations/${id}/settings?tab=alerting`);
    await assertMenuFullyClickable(page);
  });

  test("every option is clickable on a system", async ({ page }) => {
    // Same card, different scope — the clipping came from the shared
    // card style, so it was never specific to integrations.
    await logIn(page);
    const created = await page.request.post("/api/v1/systems", {
      data: { name: `e2e-menu-${Date.now().toString(36)}`, type_key: "rabbitmq" },
    });
    expect(created.ok(), `create system: ${created.status()}`).toBeTruthy();
    const id = (await created.json()).id as string;
    try {
      await page.goto(`/systems/${id}`);
      await assertMenuFullyClickable(page);
    } finally {
      await page.request.delete(`/api/v1/systems/${id}`);
    }
  });
});
