// SPDX-License-Identifier: Apache-2.0
//
// Attribute-level trim (2026-07-19): the metrics report lists each
// metric's datapoint attributes, and the Trim-ingestion panel can drop
// by attribute value — generating OTTL `datapoint` conditions next to
// the whole-metric `metric` conditions.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

test.describe("Metrics report — attribute breakdown + attribute-scoped trim", () => {
  test("report rows expand to attributes; picking a value yields a datapoint OTTL rule", async ({ page }) => {
    test.setTimeout(90_000);
    await logIn(page);
    const admin = page.request;

    // Precondition: a metric with datapoint attributes in the window
    // (the seeder's queue.depth gauge carries dimensions).
    const catalog = (await (await admin.get("/api/v1/metric-catalog?range=7d")).json()).metrics ?? [];
    test.skip(catalog.length === 0, "no metrics on this cell — seed first");
    let metric = "";
    let attrKey = "";
    for (const m of catalog.slice(0, 30)) {
      const fields = (await (await admin.get(`/api/v1/metric-fields?range=7d&metric=${encodeURIComponent(m.name)}`)).json()).fields ?? [];
      if (fields.length > 0) {
        metric = m.name;
        attrKey = fields[0].key;
        break;
      }
    }
    test.skip(!metric, "no metric with datapoint attributes in the window");

    // 1. The report lists the attribute breakdown on expand.
    await page.goto("/settings?tab=reports");
    // The seeder spreads metric points over days — use the 7d window.
    await page.locator("select.toolbar__select").last().selectOption("7d");
    const row = page.getByRole("cell", { name: new RegExp(metric.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")) }).first();
    // The metric may sit below the first render page or be used by rules
    // (the report shows unused metrics only) — fall back to the panel-only
    // checks when it isn't listed.
    const reportHasRow = await row.isVisible().catch(() => false);
    if (reportHasRow) {
      await row.click();
      await expect(page.getByRole("button", { name: new RegExp(attrKey.replace(/\./g, "\\.")) }).first()).toBeVisible();
    }

    // 2. The trim panel: open attrs on the metric, pick a value, and the
    //    generated config gains a datapoint condition.
    await page.getByRole("button", { name: /Trim ingestion/ }).first().click();
    const panel = page.locator(".card", { hasText: "Trim ingestion" });
    await panel.getByPlaceholder("Filter metrics…").fill(metric);
    await panel.getByRole("button", { name: /attrs/ }).first().click();
    await panel.getByRole("button", { name: new RegExp(attrKey.replace(/\./g, "\\.")) }).first().click();
    await panel.getByRole("button", { name: /✂/ }).first().click();

    // The rule badge appears and the YAML carries the datapoint condition.
    await expect(panel.getByText("1 attribute rule")).toBeVisible();
    await expect(panel.getByText("datapoint:", { exact: false })).toBeVisible();
    await expect(panel.getByText(`metric.name == "${metric}"`, { exact: false }).first()).toBeVisible();
  });
});
