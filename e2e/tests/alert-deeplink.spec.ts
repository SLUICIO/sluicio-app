// SPDX-License-Identifier: Apache-2.0
//
// Notification deep links: alert emails/webhooks link back with
// ?instance=<alert-instance-id> (alerting/delivery.go alertLinkPath) and
// the target page pulses the matching row (lib/useInstanceHighlight).
// Firing state is stubbed — the suite's cell has no live alerts — but the
// pages' resolution + highlight behavior is real.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

const INSTANCE = "aaaaaaaa-bbbb-cccc-dddd-eeeeffff0001";
const RULE = "aaaaaaaa-bbbb-cccc-dddd-eeeeffff0002";

test.describe("Alert notification deep links (?instance=)", () => {
  test("errors page highlights the failing check the email points at", async ({ page }) => {
    await logIn(page);
    await page.route("**/api/v1/errors?*", (route) =>
      route.fulfill({
        json: {
          window: { from: "", to: "" },
          failing_checks: [
            {
              id: INSTANCE,
              rule_id: RULE,
              rule_name: "e2e deep-link check",
              severity: "critical",
              started_at: new Date().toISOString(),
              target_kind: "global",
            },
          ],
          open_errors: [],
          services: [],
          counts: { failing_checks: 1, services_unhealthy: 0, services_errors: 0 },
        },
      }),
    );
    await page.goto(`/stuck?instance=${INSTANCE}`);
    const row = page.locator(".instance-highlight");
    await expect(row).toHaveCount(1);
    await expect(row).toContainText("e2e deep-link check");
  });

  test("alerts page highlights the rule row owning the instance", async ({ page }) => {
    await logIn(page);
    await page.route("**/api/v1/alert-instances?*", (route) =>
      route.fulfill({
        json: {
          instances: [
            {
              id: INSTANCE,
              alert_rule_id: RULE,
              rule_name: "e2e deep-link rule",
              severity: "warning",
              state: "firing",
              started_at: new Date().toISOString(),
            },
          ],
        },
      }),
    );
    await page.route("**/api/v1/alert-rules*", (route) =>
      route.fulfill({
        json: {
          rules: [
            {
              id: RULE,
              name: "e2e deep-link rule",
              description: "",
              severity: "warning",
              enabled: true,
              signal: "metric",
              spec: {},
              channel_ids: [],
            },
          ],
        },
      }),
    );
    await page.goto(`/alerts?instance=${INSTANCE}`);
    const row = page.locator(".instance-highlight");
    await expect(row).toHaveCount(1);
    await expect(row).toContainText("e2e deep-link rule");
  });

  test("a stale ?instance= (resolved/unknown) degrades to no highlight, no error", async ({ page }) => {
    await logIn(page);
    await page.goto("/stuck?instance=00000000-0000-0000-0000-000000000000");
    await expect(page.getByText("Errors").first()).toBeVisible();
    await expect(page.locator(".instance-highlight")).toHaveCount(0);
    await expect(page.locator(".alert--error")).toHaveCount(0);
  });
});
