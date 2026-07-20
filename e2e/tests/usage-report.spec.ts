// SPDX-License-Identifier: Apache-2.0
//
// Usage report (2026-07-20): Settings → Reports now spans all three
// signals. The admin-only /reports/usage endpoint says how much ingested
// data no alert rule watches, and the tab surfaces savings-suggestion
// cards ("X of Y metrics aren't used in any alert — ≈ Z/day") plus
// per-service logs/traces coverage tables.
import { test, expect, type APIRequestContext } from "@playwright/test";
import { logIn } from "./fixtures";

const VIEWER_EMAIL = "e2e-usage-viewer@sluicio.local";
const VIEWER_PASSWORD = "e2e-usage-viewer-pw1";

async function ensureViewer(admin: APIRequestContext): Promise<void> {
  const res = await admin.post("/api/v1/settings/members", {
    data: { email: VIEWER_EMAIL, name: "E2E Usage Viewer", password: VIEWER_PASSWORD, role: "viewer" },
  });
  if (!res.ok() && res.status() !== 409) {
    throw new Error(`could not provision viewer: ${res.status()}`);
  }
}

test.describe("Usage report — savings suggestions + per-signal coverage", () => {
  test("endpoint reports unused share per signal with size estimates", async ({ page }) => {
    await logIn(page);
    const res = await page.request.get("/api/v1/reports/usage?range=7d");
    expect(res.ok()).toBeTruthy();
    const body = await res.json();

    // Shape: all three signal sections with counts + byte estimates.
    for (const signal of ["metrics", "logs", "traces"] as const) {
      const rep = body[signal];
      expect(rep, signal).toBeTruthy();
      expect(typeof rep.total).toBe("number");
      expect(typeof rep.unused).toBe("number");
      expect(rep.unused).toBeLessThanOrEqual(rep.total);
      expect(typeof rep.est_bytes_per_day).toBe("number");
      expect(rep.est_bytes_per_30d).toBe(rep.est_bytes_per_day * 30);
    }

    // The seeder ships metrics no rule watches, and services with
    // logs/traces — so with seed data present, the unused share and the
    // per-service breakdown are non-empty.
    if (body.metrics.total > 0) {
      expect(body.metrics.unused).toBeGreaterThan(0);
      expect(body.metrics.est_bytes_per_day).toBeGreaterThan(0);
    }
    for (const signal of ["logs", "traces"] as const) {
      const rep = body[signal];
      if (rep.total > 0) {
        expect(rep.services?.length).toBeGreaterThan(0);
        for (const s of rep.services) {
          expect(s.service_name).toBeTruthy();
          expect(s.rows).toBeGreaterThan(0);
          expect(typeof s.covered).toBe("boolean");
        }
      }
    }
  });

  test("endpoint is admin-only", async ({ page, browser }) => {
    await logIn(page);
    await ensureViewer(page.request);
    const ctx = await browser.newContext();
    const viewerPage = await ctx.newPage();
    await logIn(viewerPage, VIEWER_EMAIL, VIEWER_PASSWORD);
    const res = await viewerPage.request.get("/api/v1/reports/usage?range=7d");
    expect([401, 403]).toContain(res.status());
    await ctx.close();
  });

  test("reports tab shows savings cards and per-service coverage", async ({ page }) => {
    await logIn(page);
    const report = await (await page.request.get("/api/v1/reports/usage?range=7d")).json();

    await page.goto("/settings?tab=reports");
    await expect(page.getByRole("heading", { name: "Usage report" })).toBeVisible();
    // Widen to the seeded window (the seeder spreads data over days).
    // Select-and-verify: a change fired mid-hydration is swallowed.
    await expect(async () => {
      await page.locator("select.toolbar__select").last().selectOption("7d");
      await expect(page.locator("select.toolbar__select").last()).toHaveValue("7d");
    }).toPass();

    if (report.metrics.total > 0 && report.metrics.unused > 0) {
      await expect(page.getByText(/aren't used in any alert/).first()).toBeVisible();
      await expect(page.getByText(/could save/).first()).toBeVisible();
    }
    if ((report.logs.services ?? []).length > 0) {
      await expect(page.getByRole("heading", { name: "Logs by service" })).toBeVisible();
      const uncoveredLog = (report.logs.services ?? []).find((s: { covered: boolean }) => !s.covered);
      if (uncoveredLog) {
        await expect(page.getByText("not covered").first()).toBeVisible();
      }
    }
    if ((report.traces.services ?? []).length > 0) {
      await expect(page.getByRole("heading", { name: "Traces by service" })).toBeVisible();
    }
  });

  test("trim panel spans logs and traces with guardrails", async ({ page }) => {
    test.setTimeout(90_000);
    await logIn(page);
    const report = await (await page.request.get("/api/v1/reports/usage?range=7d")).json();
    const logSvc = report.logs.services?.[0]?.service_name;
    const traceSvc = report.traces.services?.[0]?.service_name;
    test.skip(!logSvc || !traceSvc, "no seeded logs/traces on this cell");

    await page.goto("/settings?tab=reports");
    await expect(async () => {
      await page.locator("select.toolbar__select").last().selectOption("7d");
      await expect(page.locator("select.toolbar__select").last()).toHaveValue("7d");
    }).toPass();
    await page.getByRole("button", { name: /Trim ingestion/ }).first().click();
    const panel = page.locator(".card", { hasText: "Trim ingestion" });

    // Logs: picking a service defaults to the severity-floor trim.
    await panel.getByRole("button", { name: /^Logs/ }).click();
    await panel.locator("label", { hasText: logSvc }).locator("input[type=checkbox]").check();
    await expect(panel.getByText("log_record:")).toBeVisible();
    await expect(panel.getByText(`service.name"] == "${logSvc}"`, { exact: false }).first()).toBeVisible();
    await expect(panel.getByText("SEVERITY_NUMBER_WARN", { exact: false }).first()).toBeVisible();

    // Traces: a service feeding an integration defaults to the safe
    // sampling lever (and carries the ⚠ flag); otherwise it drops.
    await panel.getByRole("button", { name: "Clear" }).click();
    await panel.getByRole("button", { name: /^Traces/ }).click();
    const row = panel.locator("label", { hasText: traceSvc }).first();
    await row.locator("input[type=checkbox]").check();
    const mode = await row.locator("select").inputValue();
    if (mode === "sample") {
      await expect(row.getByText(/⚠ feeds/)).toBeVisible();
      await expect(panel.getByText("tail_sampling/sluicio-trim").first()).toBeVisible();
      await expect(panel.getByText("sampling_percentage: 10", { exact: false })).toBeVisible();
      await row.locator("select").selectOption("drop");
      await expect(panel.getByText("span:")).toBeVisible();
    } else {
      await expect(panel.getByText("span:")).toBeVisible();
      await row.locator("select").selectOption("sample");
      await expect(panel.getByText("tail_sampling/sluicio-trim").first()).toBeVisible();
    }
  });
});
