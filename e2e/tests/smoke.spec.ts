// SPDX-License-Identifier: Apache-2.0
//
// Smoke suite — cheap, broad "is the stack alive and wired up" checks.
// Not feature coverage; just enough to catch a dead backend, a broken
// proxy, or an app shell that white-screens on a route.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

test.describe("Smoke — stack reachability", () => {
  // cell-api is reachable and answering. install-state is the public,
  // no-auth endpoint the login page itself calls, so it's the right
  // unauthenticated readiness probe (/healthz sits behind the auth gate).
  test("cell-api answers the public install-state endpoint", async ({ request }) => {
    const base = process.env.E2E_API_URL || "http://localhost:8081";
    const res = await request.get(`${base}/api/v1/auth/install-state`);
    expect(res.ok()).toBeTruthy();
    expect(await res.json()).toHaveProperty("fresh");
  });

  // Each primary route should render the app shell (not the login page,
  // not a blank crash) once authenticated.
  const routes = ["/health", "/services", "/integrations", "/alerts", "/developers"];
  for (const route of routes) {
    test(`renders the app shell at ${route}`, async ({ page }) => {
      await logIn(page);
      await page.goto(route);
      await expect(page).toHaveURL(new RegExp(`${route}$`));
      // Still authenticated — the login card must not reappear.
      await expect(page.getByText("Sign in to Sluicio")).toBeHidden();
      // No unhandled React crash left the body empty.
      await expect(page.locator("body")).not.toBeEmpty();
    });
  }
});
