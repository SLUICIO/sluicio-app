// SPDX-License-Identifier: Apache-2.0
//
// Demo login pre-fill — when the public install-state endpoint carries a
// `prefill` block (demo cells set SLUICIO_LOGIN_PREFILL_*), the login form
// seeds the email + password fields and shows the "public demo" note. The
// endpoint is stubbed here so the test runs identically against any cell
// without touching its environment.
import { test, expect } from "@playwright/test";

test.describe("Login credential pre-fill (demo cells)", () => {
  test("prefill advertised → fields seeded + demo note shown", async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    await page.route("**/api/v1/auth/install-state", (route) =>
      route.fulfill({
        json: { fresh: false, prefill: { email: "demo@sluicio.com", password: "demodemo" } },
      }),
    );
    await page.goto("/");
    await expect(page.getByLabel("Email")).toHaveValue("demo@sluicio.com");
    await expect(page.getByLabel("Password")).toHaveValue("demodemo");
    await expect(page.getByText("Public demo environment")).toBeVisible();
    // Ready to submit in one click.
    await expect(page.getByRole("button", { name: /^Sign in$/ })).toBeEnabled();
  });

  test("no prefill (every normal install) → empty form, no note", async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    await page.route("**/api/v1/auth/install-state", (route) =>
      route.fulfill({ json: { fresh: false } }),
    );
    await page.goto("/");
    await expect(page.getByLabel("Email")).toHaveValue("");
    await expect(page.getByLabel("Password")).toHaveValue("");
    await expect(page.getByText("Public demo environment")).toHaveCount(0);
  });

  test("fresh install → hint states the seeded admin credentials", async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    await page.route("**/api/v1/auth/install-state", (route) =>
      route.fulfill({ json: { fresh: true } }),
    );
    await page.goto("/");
    await expect(page.getByText("admin@sluicio.local")).toBeVisible();
  });

  test("prefill never overwrites what the user already typed", async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    // Hold the install-state response until the user has typed.
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    await page.route("**/api/v1/auth/install-state", async (route) => {
      await gate;
      await route.fulfill({
        json: { fresh: false, prefill: { email: "demo@sluicio.com", password: "demodemo" } },
      });
    });
    await page.goto("/");
    await page.getByLabel("Email").fill("me@example.com");
    release();
    // The password field was empty, so it may seed; the typed email must survive.
    await expect(page.getByText("Public demo environment")).toBeVisible();
    await expect(page.getByLabel("Email")).toHaveValue("me@example.com");
  });
});
