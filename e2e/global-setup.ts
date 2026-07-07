// SPDX-License-Identifier: Apache-2.0
//
// Global setup: settle the cell's install state before any UI test runs.
//
// A pristine cell boots the login page into the first-run setup screen —
// and it flips there asynchronously (after /auth/install-state resolves),
// which races the logIn fixture on cold first loads. One API login here
// flips last_login_at, so every UI test deterministically gets the
// normal sign-in form. Harmless on already-used cells; a failed login
// (custom creds, cell mid-boot) is ignored — the fixture's skip-link
// fallback still covers the fresh case.
import { request } from "@playwright/test";

export default async function globalSetup() {
  const baseURL = process.env.E2E_BASE_URL || "http://localhost:5173";
  const email = process.env.E2E_ADMIN_EMAIL || "admin@sluicio.local";
  const password = process.env.E2E_ADMIN_PASSWORD || "admin";
  try {
    const ctx = await request.newContext({ baseURL });
    await ctx.post("/api/v1/auth/login", { data: { email, password } });
    await ctx.dispose();
  } catch {
    /* best-effort — see above */
  }
}
