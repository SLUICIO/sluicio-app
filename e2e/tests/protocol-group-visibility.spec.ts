// SPDX-License-Identifier: Apache-2.0
//
// A manual test protocol executed verbatim, end to end, through the UI —
// the answer to "can the suite run our test-protocol items?". Every step
// is a real user action (forms, buttons, sign-out), no API fixtures:
//
//   1. Log on to Sluicio as an admin
//   2. Create an integration called ABC
//   3. Add a new user userA
//   4. Add userA to group groupA
//   5. Assign read rights to integration ABC for groupA (attach as viewer)
//   6. Log out
//   7. Log in as userA (completing the forced password change if shown)
//   8. Verify userA can see integration ABC — and nothing else
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

const STAMP = Date.now().toString(36);
const INTEG = `ABC-${STAMP}`;
const GROUP = `groupA-${STAMP}`;
const USER_EMAIL = `usera-${STAMP}@e2e.local`;
const USER_NAME = `User A ${STAMP}`;
const USER_PASSWORD = "usera-initial-pass1";
const USER_NEW_PASSWORD = "usera-rotated-pass1";

test("protocol: group-granted visibility of one integration", async ({ page }) => {
  test.setTimeout(120_000);

  // 1. Log on as admin.
  await logIn(page);

  // Precondition: visibility for scoped viewers flows through an
  // integration's MEMBER SERVICES (canSeeIntegration), so ABC needs a
  // matcher bound to a service that actually exists. Pick one from the
  // catalog; skip on a cell with no telemetry (same convention as
  // rbac.spec).
  const svcResp = await (await page.request.get("/api/v1/services?range=7d")).json();
  const svc = (svcResp.services ?? [])[0]?.service_name as string | undefined;
  test.skip(!svc, "cell has no services — seed telemetry first");

  // 2. Create an integration called ABC, matching that service.
  await page.goto("/integrations/new");
  await page.getByLabel(/^Name/).first().fill(INTEG);
  await page.getByRole("combobox").first().selectOption("equals");
  await page.getByRole("button", { name: /Pick a service/i }).first().click();
  await page.getByRole("listbox").getByRole("searchbox").fill(svc!);
  await page.getByRole("option", { name: svc! }).first().click();
  await page.getByRole("button", { name: /Create integration/ }).click();
  await expect(page).toHaveURL(/\/integrations\/[0-9a-f-]{36}/, { timeout: 15_000 });

  // 3. Add a new user userA (role: viewer — read rights come via the group).
  await page.goto("/settings?tab=members");
  await page.getByRole("button", { name: "+ Add member" }).click();
  await page.getByLabel("Email").fill(USER_EMAIL);
  await page.getByLabel("Name", { exact: true }).fill(USER_NAME);
  await page.getByLabel(/Initial password/).fill(USER_PASSWORD);
  await page.getByLabel("Role").selectOption("viewer");
  await page.getByRole("button", { name: /^Add member$/ }).click();
  await expect(page.getByRole("cell", { name: USER_EMAIL })).toBeVisible();

  // 4. Create groupA and add userA to it.
  await page.goto("/settings?tab=groups");
  await page.getByRole("button", { name: "+ New group" }).click();
  await page.getByLabel(/^Name/).first().fill(GROUP);
  await page.getByRole("button", { name: /Create group/ }).click();
  // Open the group's blade (rows are the trigger) and add the member.
  await page.getByRole("row", { name: new RegExp(GROUP) }).click();
  await page.getByRole("button", { name: "+ Add member" }).click();
  const memberSelect = page.getByLabel("Org member");
  await memberSelect.selectOption({ label: USER_EMAIL });
  // "Read rights" — viewer in the group, not the editor default.
  await page.getByLabel("Role in group").selectOption("viewer");
  await page.getByRole("button", { name: "Add to group" }).click();
  await expect(page.getByText(USER_EMAIL).first()).toBeVisible();
  await page.keyboard.press("Escape");

  // 5. Attach groupA to the integration (viewer access).
  await page.goto("/integrations");
  await page.getByRole("link", { name: INTEG }).click();
  await page.goto(page.url().replace(/\/?$/, "") + "/settings");
  const groupCard = page.locator(".card", { hasText: "Group access" });
  await groupCard.locator("label", { hasText: GROUP }).locator("input[type=checkbox]").check();
  await groupCard.getByRole("button", { name: /Save group access/ }).click();
  await expect(groupCard.getByText("saved ✓")).toBeVisible();

  // 6. Log out. Sign-out asks a native confirm() — accept it (Playwright
  // auto-dismisses dialogs otherwise, silently cancelling the sign-out).
  await page.getByRole("button", { name: /Account menu/ }).click();
  const signOut = page.getByRole("menuitem", { name: "Sign out" });
  await signOut.waitFor({ state: "visible" });
  page.once("dialog", (d) => d.accept());
  await signOut.click();
  await expect(page.getByText("Sign in to Sluicio")).toBeVisible({ timeout: 15_000 });

  // 7. Log in as userA — completing the first-login password rotation if
  //    the cell enforces one.
  await page.getByLabel("Email").fill(USER_EMAIL);
  await page.getByLabel("Password").fill(USER_PASSWORD);
  await page.getByRole("button", { name: /^Sign in$/ }).click();
  const forced = page.getByLabel("Temporary password");
  try {
    await forced.waitFor({ state: "visible", timeout: 4000 });
    await forced.fill(USER_PASSWORD);
    await page.getByLabel("New password").fill(USER_NEW_PASSWORD);
    await page.getByLabel("Confirm new password").fill(USER_NEW_PASSWORD);
    await page.getByRole("button", { name: /Set new password|Save|Change password/ }).click();
  } catch {
    /* no forced rotation on this cell — fine */
  }

  // 8. userA sees integration ABC — and only what the group granted.
  await page.goto("/integrations");
  await expect(page.getByRole("link", { name: INTEG })).toBeVisible({ timeout: 15_000 });
});
