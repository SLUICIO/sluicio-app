// SPDX-License-Identifier: Apache-2.0
//
// First-run "create your admin account" screen. A pristine install
// (install-state fresh:true) boots into a setup form instead of the
// login form; submitting personalizes the seeded admin via the public
// bootstrap endpoint, which self-seals after the first-ever login.
// The dev/CI cells are never fresh, so freshness is stubbed here —
// but the POST goes to the real backend, proving the 409 seal.
import { test, expect } from "@playwright/test";

test.describe("First-run admin setup", () => {
  test("fresh install boots into the setup screen; skip link reaches sign-in", async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    await page.route("**/api/v1/auth/install-state", (route) =>
      route.fulfill({ json: { fresh: true } }),
    );
    await page.goto("/");
    await expect(page.getByText("Welcome to Sluicio")).toBeVisible();
    await expect(page.getByText("Create your admin account to finish setting up")).toBeVisible();
    await expect(page.getByLabel("Name")).toBeVisible();
    await expect(page.getByLabel("Confirm password")).toBeVisible();
    // The skip path lands on the normal sign-in form.
    await page.getByRole("button", { name: /sign in with the seeded admin account/i }).click();
    await expect(page.getByText("Sign in to Sluicio")).toBeVisible();
  });

  test("bootstrap endpoint is sealed once the cell has logins (409 → sign-in)", async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    // On a genuinely fresh cell the POST would SUCCEED and replace the
    // seeded admin — sabotaging every later logIn in the run. Only probe
    // the seal where a seal exists. (page.request bypasses page.route,
    // so this reads the real install state, not the stub below.)
    const real = await (await page.request.get("/api/v1/auth/install-state")).json();
    test.skip(real.fresh === true, "cell is pristine — no seal to probe yet");
    await page.route("**/api/v1/auth/install-state", (route) =>
      route.fulfill({ json: { fresh: true } }),
    );
    await page.goto("/");
    await expect(page.getByText("Welcome to Sluicio")).toBeVisible();
    await page.getByLabel("Name").fill("E2E Probe");
    await page.getByLabel("Email").fill("e2e-bootstrap-probe@sluicio.local");
    await page.getByLabel("Password", { exact: true }).fill("probe-password-1");
    await page.getByLabel("Confirm password").fill("probe-password-1");
    // Real POST — the cell has logins, so the server must refuse.
    await page.getByRole("button", { name: "Create admin account" }).click();
    await expect(page.getByText("This install is already set up — sign in below.")).toBeVisible();
    await expect(page.getByText("Sign in to Sluicio")).toBeVisible();
  });

  test("mismatched passwords are caught before any request", async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    await page.route("**/api/v1/auth/install-state", (route) =>
      route.fulfill({ json: { fresh: true } }),
    );
    let posted = false;
    await page.route("**/api/v1/auth/bootstrap-admin", (route) => {
      posted = true;
      return route.continue();
    });
    await page.goto("/");
    // Fill-and-verify: on a slow CI runner a fill can land during
    // hydration and be swallowed by the controlled input's first
    // render, leaving the submit button disabled forever.
    for (const [label, value] of [
      ["Email", "x@example.com"],
      ["Password", "password-one-1"],
      ["Confirm password", "password-two-2"],
    ] as const) {
      const input = page.getByLabel(label, { exact: true });
      await expect(async () => {
        await input.fill(value);
        await expect(input).toHaveValue(value);
      }).toPass({ timeout: 10_000 });
    }
    await expect(page.getByRole("button", { name: "Create admin account" })).toBeEnabled();
    await page.getByRole("button", { name: "Create admin account" }).click();
    await expect(page.getByText("Passwords don't match.")).toBeVisible();
    expect(posted).toBe(false);
  });
});
