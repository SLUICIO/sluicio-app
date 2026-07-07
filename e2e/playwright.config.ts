// SPDX-License-Identifier: Apache-2.0
import { defineConfig, devices } from "@playwright/test";

// Where the SPA is served. Locally and in CI we point Playwright at the
// Vite dev server (port 5173), which proxies /api → cell-api on :8081.
// Override to test a built/deployed frontend:
//   E2E_BASE_URL=https://cell.example.com npm test
const baseURL = process.env.E2E_BASE_URL || "http://localhost:5173";

// When E2E_BASE_URL is set we assume the frontend is already served
// elsewhere and skip starting Vite ourselves.
const manageFrontend = !process.env.E2E_BASE_URL;

export default defineConfig({
  testDir: "./tests",
  // Settles a pristine cell's install state (one API login) so UI tests
  // deterministically get the sign-in form, not the first-run screen.
  globalSetup: "./global-setup",
  // One retry in CI smooths over the occasional cold-start flake; none
  // locally so a real failure shows up immediately.
  retries: process.env.CI ? 1 : 0,
  // Fail the CI run if someone leaves a `test.only` behind.
  forbidOnly: !!process.env.CI,
  fullyParallel: true,
  reporter: process.env.CI
    ? [["list"], ["html", { open: "never" }], ["github"]]
    : [["list"], ["html", { open: "never" }]],
  timeout: 30_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL,
    // Artefacts only for failures — keeps the happy path fast.
    trace: "on-first-retry",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
      // ee-features flips cell-wide state (the org MFA policy) that would
      // 403 every other spec running beside it — it gets its own phase.
      testIgnore: /ee-features\.spec\.ts/,
    },
    {
      name: "ee",
      use: { ...devices["Desktop Chrome"] },
      testMatch: /ee-features\.spec\.ts/,
      dependencies: ["chromium"],
    },
  ],
  // Start the Vite dev server for the run. It proxies /api to the
  // cell-api the compose stack brings up, so the backend must already
  // be running (`make dev-up`). reuseExistingServer lets a dev who
  // already has `npm run dev` going skip the extra spawn.
  webServer: manageFrontend
    ? {
        command: "npm --prefix ../frontend run dev",
        url: baseURL,
        timeout: 120_000,
        reuseExistingServer: !process.env.CI,
      }
    : undefined,
});
