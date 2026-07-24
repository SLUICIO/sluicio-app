// SPDX-License-Identifier: Apache-2.0
//
// Announcements + maintenance windows (docs/maintenance-and-announcements-design.md):
//   - a cell-wide announcement shows as a banner for users, dismissal is
//     per-user and server-side (sticks across reloads)
//   - operators publish/remove announcements from Settings → System (the
//     org-scoped section was removed 2026-07-24)
//   - maintenance windows are scheduled from Alerts → Maintenance; an
//     org-wide window announces itself and shows the active strip
//   - API guardrails: bounded windows, editor vs admin scopes
// Engine-level suppression semantics are covered by Go tests
// (TestMaintenanceWindowCovers) — not re-proven here.
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { logIn, ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";

async function apiLogin(email: string, password: string): Promise<APIRequestContext> {
  const ctx = await pwRequest.newContext({ baseURL: BASE_URL });
  const res = await ctx.post("/api/v1/auth/login", { data: { email, password } });
  if (!res.ok()) throw new Error(`login ${email}: ${res.status()}`);
  return ctx;
}

test.describe("Announcements", () => {
  test("banner shows, dismissal sticks across reloads", async ({ page }) => {
    const admin = await apiLogin(ADMIN_EMAIL, ADMIN_PASSWORD);
    const msg = `e2e announcement ${Date.now()}`;
    // Cell-wide is the single announcement surface (org-scoped management
    // was removed 2026-07-24) — users still see it as an in-app banner.
    const created = await (
      await admin.post("/api/v1/operator/announcements", {
        data: { message: msg, severity: "warning" },
      })
    ).json();
    try {
      await logIn(page);
      await expect(page.getByText(msg)).toBeVisible();
      // The dismissal POST is asynchronous — wait for it to land before
      // reloading, or under parallel-suite load the reload can beat it
      // and the banner "returns" (same race class as the metadata /
      // column-preference saves).
      const dismissed = page.waitForResponse(
        (r) => r.url().includes("/dismiss") && r.request().method() === "POST" && r.ok(),
      );
      await page
        .locator(".alert", { hasText: msg })
        .getByRole("button", { name: "Dismiss announcement" })
        .click();
      await expect(page.getByText(msg)).toHaveCount(0);
      await dismissed;
      // Server-side dismissal: still gone after a full reload.
      await page.reload();
      await expect(page.getByText("Dashboard").first()).toBeVisible();
      await expect(page.getByText(msg)).toHaveCount(0);
    } finally {
      await admin.delete(`/api/v1/operator/announcements/${created.id}`);
    }
  });

  test("login page shows only cell-wide announcements flagged for it; browser dismissal sticks", async ({ browser }) => {
    const admin = await apiLogin(ADMIN_EMAIL, ADMIN_PASSWORD);
    const loginMsg = `e2e login banner ${Date.now()}`;
    const plainMsg = `e2e plain cell banner ${Date.now()}`;
    const onLogin = await (
      await admin.post("/api/v1/operator/announcements", {
        data: { message: loginMsg, severity: "warning", show_on_login: true },
      })
    ).json();
    const plain = await (
      await admin.post("/api/v1/operator/announcements", {
        data: { message: plainMsg, severity: "info" },
      })
    ).json();
    try {
      // Org-scoped announcement management is gone entirely — the old
      // endpoint answers 404 (cell-wide is the single surface).
      const refused = await admin.post("/api/v1/settings/announcements", {
        data: { message: "should not exist", show_on_login: true },
      });
      expect([404, 405]).toContain(refused.status());

      // The public endpoint returns ONLY the flagged one, minimal payload.
      const anon = await pwRequest.newContext({ baseURL: BASE_URL });
      const pub = await (await anon.get("/api/v1/announcements/login")).json();
      const mine = (pub.announcements ?? []).filter(
        (a: { id: string }) => a.id === onLogin.id || a.id === plain.id,
      );
      expect(mine.map((a: { id: string }) => a.id)).toEqual([onLogin.id]);
      expect(mine[0].created_by).toBeUndefined();
      expect(mine[0].org_id).toBeUndefined();
      await anon.dispose();

      // A logged-out browser sees the banner on the sign-in page.
      const ctx = await browser.newContext();
      const anonPage = await ctx.newPage();
      await anonPage.goto("/");
      await expect(anonPage.getByText(loginMsg)).toBeVisible();
      await expect(anonPage.getByText(plainMsg)).toHaveCount(0);
      // Per-browser dismissal (localStorage) survives a reload.
      await anonPage
        .locator(".alert", { hasText: loginMsg })
        .getByRole("button", { name: "Dismiss announcement" })
        .click();
      await expect(anonPage.getByText(loginMsg)).toHaveCount(0);
      await anonPage.reload();
      await expect(anonPage.getByText(/Sign in to Sluicio|Welcome to Sluicio/)).toBeVisible();
      await expect(anonPage.getByText(loginMsg)).toHaveCount(0);
      await ctx.close();
    } finally {
      await admin.delete(`/api/v1/operator/announcements/${onLogin.id}`);
      await admin.delete(`/api/v1/operator/announcements/${plain.id}`);
      await admin.dispose();
    }
  });

  test("cell-wide announcements live on Settings → System, not the Operator page", async ({ page }) => {
    await logIn(page); // suite admin is a cell operator
    await page.goto("/settings?tab=system");
    await expect(page.getByRole("heading", { name: "Cell-wide announcements" })).toBeVisible();
    await page.goto("/operator");
    await expect(page.getByRole("heading", { name: /announcements/i })).toHaveCount(0);
  });

  test("operator publishes and removes from Settings → System", async ({ page }) => {
    await logIn(page);
    await page.goto("/settings?tab=system");
    const msg = `e2e settings announcement ${Date.now()}`;
    await page.getByPlaceholder(/Planned maintenance tonight/).fill(msg);
    await page.getByRole("button", { name: "Publish" }).click();
    await expect(page.locator("table").getByText(msg)).toBeVisible();
    page.once("dialog", (d) => d.accept());
    await page
      .locator("tr", { hasText: msg })
      .getByRole("button", { name: "Remove" })
      .click();
    await expect(page.locator("table").getByText(msg)).toHaveCount(0);
  });
});

test.describe("Maintenance windows", () => {
  // Leftover active windows from a crashed run would silence the cell for
  // hours and pollute the strip assertions — sweep our fixtures first.
  test.beforeEach(async () => {
    const admin = await apiLogin(ADMIN_EMAIL, ADMIN_PASSWORD);
    const { windows } = await (await admin.get("/api/v1/maintenance-windows")).json();
    for (const w of windows ?? []) {
      if (w.active && String(w.name).startsWith("e2e window")) {
        await admin.delete(`/api/v1/maintenance-windows/${w.id}`);
      }
    }
    await admin.dispose();
  });

  test("org-wide window: schedule from the UI, strip + banner, end early", async ({ page }) => {
    const name = `e2e window ${Date.now()}`;
    await logIn(page);
    await page.goto("/alerts");
    await page.getByRole("button", { name: "Maintenance" }).click();
    try {
      await page.getByRole("button", { name: "Schedule maintenance" }).click();
      await page.getByPlaceholder("July release").fill(name);
      await page.getByRole("radio", { name: "The whole organization" }).check();
      // Q2: org-wide defaults the announcement checkbox ON.
      await expect(page.getByRole("checkbox", { name: /Show a banner/ })).toBeChecked();
      await page.getByRole("button", { name: "Start maintenance" }).click();

      // Row is ACTIVE, the page strip names the window, and the
      // auto-announcement banner renders for users.
      await expect(page.locator("tr", { hasText: name }).getByText("ACTIVE")).toBeVisible();
      await expect(page.locator(".alert--warn", { hasText: "Maintenance active" })).toContainText(name);
      await expect(page.getByText(`Maintenance: ${name}`)).toBeVisible();

      // End it now — this window leaves the strip and its banner goes.
      page.once("dialog", (d) => d.accept());
      await page.locator("tr", { hasText: name }).getByRole("button", { name: "End now" }).click();
      await expect(page.locator(".alert--warn", { hasText: name })).toHaveCount(0);
      await expect(page.getByText(`Maintenance: ${name}`)).toHaveCount(0);
    } finally {
      // Belt and braces: never leave an org-wide silence behind.
      const admin = await apiLogin(ADMIN_EMAIL, ADMIN_PASSWORD);
      const { windows } = await (await admin.get("/api/v1/maintenance-windows")).json();
      for (const w of windows ?? []) {
        if (w.active && w.name === name) await admin.delete(`/api/v1/maintenance-windows/${w.id}`);
      }
      await admin.dispose();
    }
  });

  test("API guardrails: bounded windows, admin-only all_org", async () => {
    const admin = await apiLogin(ADMIN_EMAIL, ADMIN_PASSWORD);
    const in1h = new Date(Date.now() + 3600_000).toISOString();
    const in9d = new Date(Date.now() + 9 * 24 * 3600_000).toISOString();

    // ends_at is mandatory.
    const unbounded = await admin.post("/api/v1/maintenance-windows", {
      data: { name: "e2e unbounded", scope: { kind: "all_org" } },
    });
    expect(unbounded.status()).toBe(400);

    // Longer than 7 days is refused.
    const tooLong = await admin.post("/api/v1/maintenance-windows", {
      data: { name: "e2e too long", ends_at: in9d, scope: { kind: "all_org" } },
    });
    expect(tooLong.status()).toBe(400);

    // An entities scope needs at least one selector.
    const empty = await admin.post("/api/v1/maintenance-windows", {
      data: { name: "e2e empty scope", ends_at: in1h, scope: { kind: "entities" } },
    });
    expect(empty.status()).toBe(400);

    // Editors may silence scoped windows but not the whole org.
    const email = "e2e-mw-editor@sluicio.local";
    const pw = "e2e-mw-editor-pw1";
    const add = await admin.post("/api/v1/settings/members", {
      data: { email, name: "E2E MW Editor", password: pw, role: "editor" },
    });
    if (!add.ok() && add.status() !== 409) throw new Error(`provision editor: ${add.status()}`);
    const editor = await apiLogin(email, pw);
    const orgWide = await editor.post("/api/v1/maintenance-windows", {
      data: { name: "e2e editor org-wide", ends_at: in1h, scope: { kind: "all_org" } },
    });
    expect(orgWide.status()).toBe(403);
    const scoped = await editor.post("/api/v1/maintenance-windows", {
      data: { name: "e2e editor scoped", ends_at: in1h, scope: { kind: "entities", service_names: ["e2e-svc"] } },
    });
    expect(scoped.status()).toBe(201);
    const win = await scoped.json();
    expect((await editor.delete(`/api/v1/maintenance-windows/${win.id}`)).status()).toBe(204);
  });
});
