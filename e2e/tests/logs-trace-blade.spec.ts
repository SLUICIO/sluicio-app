// SPDX-License-Identifier: Apache-2.0
//
// Clicking a trace id on the Logs page opens the trace as a slide-over
// blade (TraceDrawer) — same pattern as the integration Messages view —
// instead of navigating away from the filtered log list. Covered from
// both entry points: the row's trace pill and the log-details drawer's
// "View trace" action. API responses are stubbed so the test doesn't
// depend on seeded telemetry.
import { test, expect, type Page } from "@playwright/test";
import { logIn } from "./fixtures";

const TID = "abcdef0123456789abcdef0123456789";
const SID = "0123456789abcdef";

async function stubLogsAndTrace(page: Page) {
  const now = new Date().toISOString();
  await page.route("**/api/v1/logs?*", (route) =>
    route.fulfill({
      json: {
        window: "1h",
        logs: [
          {
            log_id: "e2e-log-1",
            timestamp: now,
            observed_timestamp: now,
            trace_id: TID,
            span_id: SID,
            severity_number: 17,
            severity_text: "ERROR",
            service_name: "e2e-log-svc",
            body: "e2e: downstream timeout",
            attributes: { team: "e2e" },
            log_attributes: { team: "e2e" },
            resource_attributes: { "service.name": "e2e-log-svc" },
          },
        ],
      },
    }),
  );
  await page.route(`**/api/v1/traces/${TID}`, (route) =>
    route.fulfill({
      json: {
        trace_id: TID,
        spans: [
          {
            timestamp: now,
            trace_id: TID,
            span_id: SID,
            service_name: "e2e-log-svc",
            span_name: "GET /v1/orders",
            span_kind: "Server",
            status_code: "Error",
            duration_ms: 123,
            attributes: { "http.request.method": "GET" },
          },
        ],
      },
    }),
  );
  await page.route(`**/api/v1/traces/${TID}/completion-firings`, (route) =>
    route.fulfill({ json: { firings: [] } }),
  );
}

test("trace pill on a log row opens the trace blade in place", async ({ page }) => {
  await logIn(page);
  await stubLogsAndTrace(page);
  await page.goto("/logs");

  await page.locator(".trace-pill").first().click();

  const blade = page.getByRole("dialog", { name: "Trace detail" });
  await expect(blade).toBeVisible();
  await expect(blade).toContainText(TID);
  await expect(blade).toContainText("GET /v1/orders");
  // Still on the Logs page — the blade slides over it, no navigation.
  await expect(page).toHaveURL(/\/logs/);
  // "open full view" remains the escape hatch to /traces/:id.
  await expect(blade.getByRole("link", { name: /open full view/ })).toBeVisible();

  await blade.getByRole("button", { name: /close/i }).click();
  await expect(blade).toHaveCount(0);
});

test("full view from Logs gets a breadcrumb linking back to the filtered list", async ({ page }) => {
  await logIn(page);
  await stubLogsAndTrace(page);
  await page.goto("/logs?logq=timeout");

  await page.locator(".trace-pill").first().click();
  const blade = page.getByRole("dialog", { name: "Trace detail" });
  await blade.getByRole("link", { name: /open full view/ }).click();

  await expect(page).toHaveURL(new RegExp(`/traces/${TID}`));
  const crumbs = page.getByLabel("Breadcrumb");
  await expect(crumbs.getByRole("link", { name: "Logs" })).toBeVisible();
  await expect(crumbs).toContainText(`Trace ${TID.slice(0, 12)}`);

  // The origin crumb returns to the exact filtered list, query intact.
  await crumbs.getByRole("link", { name: "Logs" }).click();
  await expect(page).toHaveURL(/\/logs\?logq=timeout/);
});

test("log details drawer's View trace opens the blade, not /traces", async ({ page }) => {
  await logIn(page);
  await stubLogsAndTrace(page);
  await page.goto("/logs");

  // Open the log-details drawer by clicking the row body.
  await page.getByText("e2e: downstream timeout").first().click();
  const drawer = page.getByLabel("Log details");
  await expect(drawer).toBeVisible();

  await drawer.getByRole("button", { name: /View trace/ }).click();
  const blade = page.getByRole("dialog", { name: "Trace detail" });
  await expect(blade).toBeVisible();
  await expect(blade).toContainText(TID);
  await expect(page).toHaveURL(/\/logs/);
});
