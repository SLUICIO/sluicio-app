// SPDX-License-Identifier: Apache-2.0
//
// SearchableSelect (every typeahead in the product):
//   - opening it focuses the filter input — click, then just type
//   - the popover stays inside the viewport; near the bottom of a short
//     window it flips upward and clamps its list, so the last options
//     remain reachable
// Exercised on /systems via the create form's type picker. It used to
// run against the integration rule editor, which now builds its own list
// inside a pill popover - a popover inside a popover fights over the
// click that closes it - so the component under test is no longer there.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

test("typeahead focuses its filter input on open", async ({ page }) => {
  await logIn(page);
  await page.goto("/systems");
  await page.getByRole("button", { name: /New system/i }).first().click();
  await page.getByRole("button", { name: /No type/i }).first().click();
  const filter = page.getByRole("listbox").getByRole("searchbox");
  await expect(filter).toBeVisible();
  await expect(filter).toBeFocused();
  // Typing works immediately, no extra click.
  await page.keyboard.type("abc");
  await expect(filter).toHaveValue("abc");
});

test("typeahead popover stays inside a short viewport", async ({ page }) => {
  await page.setViewportSize({ width: 1100, height: 500 });
  await logIn(page);
  await page.goto("/systems");
  await page.getByRole("button", { name: /New system/i }).first().click();
  const trigger = page.getByRole("button", { name: /No type/i }).first();
  await trigger.scrollIntoViewIfNeeded();
  await trigger.click();
  const pop = page.getByRole("listbox");
  await expect(pop).toBeVisible();
  const box = await pop.boundingBox();
  expect(box).toBeTruthy();
  expect(box!.y).toBeGreaterThanOrEqual(0);
  expect(box!.y + box!.height).toBeLessThanOrEqual(500 + 1);
});
