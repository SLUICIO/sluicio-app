// SPDX-License-Identifier: Apache-2.0
//
// Audit log (Enterprise) — coverage for the searchable audit trail in
// Settings → Audit log: events are recorded (login, config changes),
// admins can answer "what did user X do between T1 and T2" via the
// actor / action / time filters, details expand per row, and the CSV
// export honours the active filters.
//
// The whole suite self-skips on cells without the `audit_log`
// entitlement (Community builds no-op the recorder), so it's safe in
// any environment.
import { test, expect, type Page } from "@playwright/test";
import { logIn, ADMIN_EMAIL } from "./fixtures";

async function auditEntitled(page: Page): Promise<boolean> {
  const res = await page.request.get("/api/v1/license");
  if (!res.ok()) return false;
  const body = await res.json();
  return Boolean(body?.features?.audit_log);
}

async function openAuditTab(page: Page): Promise<void> {
  await page.goto("/settings?tab=audit");
  await expect(page.getByRole("heading", { name: "Audit log" })).toBeVisible();
}

const rows = (page: Page) => page.locator("table tbody tr");

test.describe("Audit log (EE)", () => {
  test.beforeEach(async ({ page }) => {
    await logIn(page);
    test.skip(!(await auditEntitled(page)), "cell has no audit_log entitlement");
  });

  test("login events are recorded and searchable by actor + action", async ({ page }) => {
    // The logIn in beforeEach guarantees at least one fresh login.succeeded.
    await openAuditTab(page);
    await expect(rows(page).first()).toBeVisible();

    await page.getByLabel("Actor", { exact: true }).fill(ADMIN_EMAIL.split("@")[0]);
    await page.getByLabel("Action", { exact: true }).fill("login.");
    // Two debounced fetches (actor, then action) land independently — poll
    // until EVERY row matches, not just the first, or we can read the
    // actor-only result set mid-flight.
    await expect(async () => {
      const texts = await rows(page).allInnerTexts();
      expect(texts.length).toBeGreaterThan(0);
      for (const t of texts) expect(t).toMatch(/login\.(succeeded|failed)/);
    }).toPass({ timeout: 8_000 });
  });

  test("config changes are audited with metadata in the detail row", async ({ page }) => {
    // Mutate org config through the real API from the browser session:
    // create + delete a tag, then find both actions in the log.
    const slug = `e2e-audit-${Date.now()}`;
    const created = await page.request.post("/api/v1/tags", {
      data: { slug, name: "E2E audit probe", color: "#5566ee" },
    });
    expect(created.ok()).toBeTruthy();
    const tag = await created.json();
    const deleted = await page.request.delete(`/api/v1/tags/${tag.id}`);
    expect(deleted.ok()).toBeTruthy();

    await openAuditTab(page);
    await page.getByLabel("Action", { exact: true }).fill("tag.");
    await expect(rows(page).first()).toContainText("tag.deleted", { timeout: 5_000 });
    await expect(rows(page).nth(1)).toContainText("tag.created");

    // Expand the tag.created row — the detail JSON carries the metadata.
    await rows(page).nth(1).click();
    await expect(page.locator("table pre")).toContainText(slug);
  });

  test("time-range filters bound the results", async ({ page }) => {
    await openAuditTab(page);
    // A window well in the past must be empty…
    await page.getByLabel("From", { exact: true }).fill("2020-01-01T00:00");
    await page.getByLabel("To", { exact: true }).fill("2020-01-02T00:00");
    await expect(page.getByText("No audit entries match these filters.")).toBeVisible({
      timeout: 5_000,
    });
    // …and Clear restores the listing.
    await page.getByRole("button", { name: "Clear" }).click();
    await expect(rows(page).first()).toBeVisible({ timeout: 5_000 });
  });

  test("CSV export honours the active filters", async ({ page }) => {
    const res = await page.request.get("/api/v1/audit-log?format=csv&action=login.");
    expect(res.ok()).toBeTruthy();
    expect(res.headers()["content-type"]).toContain("text/csv");
    const csv = await res.text();
    const lines = csv.trim().split("\n");
    expect(lines[0]).toContain("occurred_at");
    expect(lines.length).toBeGreaterThan(1);
    // Every data row is a login event (filter applied server-side).
    for (const line of lines.slice(1)) expect(line).toMatch(/login\.(succeeded|failed)/);
  });

  test("API rejects malformed filters", async ({ page }) => {
    expect((await page.request.get("/api/v1/audit-log?from=yesterday")).status()).toBe(400);
    expect((await page.request.get("/api/v1/audit-log?actor_id=nope")).status()).toBe(400);
  });

  test("scrolling lazy-loads further pages", async ({ page }) => {
    await openAuditTab(page);
    const container = page.getByTestId("audit-scroll");
    await expect(container.locator("tbody tr").first()).toBeVisible();
    const before = await container.locator("tbody tr").count();
    test.skip(before < 100, "cell has fewer than one page of audit entries");
    await container.evaluate((el) => {
      el.scrollTop = el.scrollHeight;
    });
    await expect
      .poll(() => container.locator("tbody tr").count(), { timeout: 5_000 })
      .toBeGreaterThan(before);
  });

  test("renames are audited and actor-id filter spans them", async ({ page }) => {
    // Rename self via the real API, then confirm the transition entry.
    const me = await (await page.request.get("/api/v1/me")).json();
    const original = me.user.name;
    const temp = `${original} (e2e)`;
    expect((await page.request.patch("/api/v1/me", { data: { name: temp } })).ok()).toBeTruthy();
    try {
      const trail = await page.request.get(
        "/api/v1/audit-log?action=user.profile_updated&limit=1",
      );
      const { entries } = await trail.json();
      expect(entries[0].metadata?.old_name).toBe(original);
      expect(entries[0].metadata?.new_name).toBe(temp);
      // actor_id filter returns entries regardless of the name they were
      // written under (the id is stable across renames).
      const byId = await page.request.get(
        `/api/v1/audit-log?actor_id=${me.user.id}&limit=200`,
      );
      const names = new Set(
        (await byId.json()).entries.map((e: { actor_name: string }) => e.actor_name),
      );
      expect(names.size).toBeGreaterThan(1);
    } finally {
      await page.request.patch("/api/v1/me", { data: { name: original } });
    }
  });

  test("hash chain verifies intact from the UI", async ({ page }) => {
    await openAuditTab(page);
    await page.getByRole("button", { name: "Verify integrity" }).click();
    const result = page.getByTestId("audit-verify-result");
    await expect(result).toBeVisible({ timeout: 10_000 });
    await expect(result).toContainText("Chain intact");
  });

  test("exporting the log is itself audited", async ({ page }) => {
    const res = await page.request.get("/api/v1/audit-log?format=csv&action=login.");
    expect(res.ok()).toBeTruthy();
    // The export must have left an audit_log.exported entry naming the scope.
    const trail = await page.request.get("/api/v1/audit-log?action=audit_log.exported&limit=1");
    const { entries } = await trail.json();
    expect(entries.length).toBe(1);
    expect(entries[0].metadata?.action).toBe("login.");
  });

  test("audit retention is configurable and round-trips", async ({ page }) => {
    const before = await (await page.request.get("/api/v1/cell-settings/retention")).json();
    expect(before.audit_days).toBeGreaterThan(0);
    // EE cell (guaranteed by beforeEach skip): raising past the free cap works.
    const set = await page.request.patch("/api/v1/cell-settings/retention", {
      data: { audit_days: 365 },
    });
    expect(set.ok()).toBeTruthy();
    const after = await (await page.request.get("/api/v1/cell-settings/retention")).json();
    expect(after.audit_days).toBe(365);
    expect(after.audit_configurable).toBe(true);
    // Restore whatever the cell had configured.
    await page.request.patch("/api/v1/cell-settings/retention", {
      data: { audit_days: before.audit_days },
    });
  });
});
