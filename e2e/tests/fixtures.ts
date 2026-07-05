// SPDX-License-Identifier: Apache-2.0
//
// Shared constants and helpers for the e2e suite. Keeping login here
// means each spec asserts behaviour, not boilerplate.
import { expect, type Page } from "@playwright/test";

// The seed admin every fresh Sluicio cell ships with. cell-api's
// BootstrapSeedAdminPassword sets this on first boot (see
// services/cell-api/cmd/cell-api/main.go). Override for a non-seed
// environment via env.
export const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@sluicio.local";
export const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "admin";

// logIn drives the real login form and waits for the post-login
// redirect to /health. Fails loudly if credentials are rejected.
export async function logIn(
  page: Page,
  email = ADMIN_EMAIL,
  password = ADMIN_PASSWORD,
): Promise<void> {
  await page.goto("/");
  await expect(page.getByText("Sign in to Sluicio")).toBeVisible();

  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: /^Sign in$/ }).click();

  // index → /health on success. This is the contract that proves the
  // whole chain worked: form → POST /auth/login → session cookie → /me.
  await expect(page).toHaveURL(/\/health$/, { timeout: 15_000 });
}
