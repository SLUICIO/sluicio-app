// SPDX-License-Identifier: Apache-2.0
//
// Executable counterpart of docs/testing/protocols/auth-login.md.
// Each test() below maps to a numbered case in that protocol so the
// manual checklist and the automated suite never drift apart.
import { test, expect } from "@playwright/test";
import { ADMIN_EMAIL, ADMIN_PASSWORD, logIn } from "./fixtures";

test.describe("Authentication — local password login", () => {
  // Case 1: the login page renders for an unauthenticated visitor.
  test("shows the sign-in form when not logged in", async ({ page }) => {
    await page.goto("/");
    await expect(page.getByText("Sign in to Sluicio")).toBeVisible();
    await expect(page.getByLabel("Email")).toBeVisible();
    await expect(page.getByLabel("Password")).toBeVisible();
    await expect(page.getByRole("button", { name: /^Sign in$/ })).toBeVisible();
  });

  // Case 2: valid seed-admin credentials sign in and land on /health.
  test("logs in with the seed admin and lands on Health", async ({ page }) => {
    await logIn(page, ADMIN_EMAIL, ADMIN_PASSWORD);
    await expect(page).toHaveURL(/\/health$/);
    // The login card must be gone once authenticated.
    await expect(page.getByText("Sign in to Sluicio")).toBeHidden();
  });

  // Case 3: wrong password is rejected with a friendly message and no
  // navigation away from the login page.
  test("rejects an invalid password", async ({ page }) => {
    await page.goto("/");
    await page.getByLabel("Email").fill(ADMIN_EMAIL);
    await page.getByLabel("Password").fill("definitely-wrong");
    await page.getByRole("button", { name: /^Sign in$/ }).click();

    await expect(page.getByText("Invalid email or password.")).toBeVisible();
    await expect(page.getByText("Sign in to Sluicio")).toBeVisible();
  });

  // Case 4: the session survives a full page reload (cookie persisted).
  test("keeps the session across a reload", async ({ page }) => {
    await logIn(page);
    await page.reload();
    await expect(page).toHaveURL(/\/health$/);
    await expect(page.getByText("Sign in to Sluicio")).toBeHidden();
  });
});
