// SPDX-License-Identifier: Apache-2.0
//
// Dashboards × RBAC — nobody sees a dashboard or a widget they weren't
// granted (RBAC v2 §5.2 A′: group_id NULL = org-wide, group_id set =
// team dashboard visible to members + org editors/admins).
//
// The widget-data angle: Health-page cards render from
// /api/v1/integrations, which is visibility-filtered server-side — so a
// dashboard ITEM referencing an out-of-scope integration must never
// materialise into a rendered widget or fetchable data for that caller.
import { test, expect, type APIRequestContext, type Browser } from "@playwright/test";
import { logIn } from "./fixtures";

const VIEWER_EMAIL = "e2e-dash-viewer@sluicio.local";
const VIEWER_PASSWORD = "e2e-dash-viewer-pw1";
const EDITOR_EMAIL = "e2e-dash-teameditor@sluicio.local";
const EDITOR_PASSWORD = "e2e-dash-teameditor-pw1";

async function ensureUser(admin: APIRequestContext, email: string, name: string, password: string): Promise<string> {
  const res = await admin.post("/api/v1/settings/members", {
    data: { email, name, password, role: "viewer" },
  });
  if (!res.ok() && res.status() !== 409) throw new Error(`provision ${email}: ${res.status()}`);
  const members = await (await admin.get("/api/v1/settings/members")).json();
  const uid = (members.members ?? []).find(
    (m: { user: { email: string; id: string } }) => m.user.email === email,
  )?.user.id;
  if (!uid) throw new Error(`no user id for ${email}`);
  return uid;
}

async function userPage(browser: Browser, email: string, password: string) {
  const page = await (await browser.newContext()).newPage();
  await logIn(page, email, password);
  return page;
}

async function makeGroup(admin: APIRequestContext, slug: string, name: string): Promise<string> {
  // Recreate for a clean slate (groups cascade memberships + policies).
  const list = await (await admin.get("/api/v1/settings/groups")).json();
  for (const g of list.groups ?? []) {
    if (g.slug === slug) await admin.delete(`/api/v1/settings/groups/${g.id}`);
  }
  const res = await admin.post("/api/v1/settings/groups", { data: { slug, name } });
  expect(res.ok()).toBeTruthy();
  return (await res.json()).id;
}

async function makeDashboard(
  ctx: APIRequestContext,
  body: { name: string; groupId?: string; items?: unknown[] },
): Promise<{ id: string; status: number }> {
  const res = await ctx.post("/api/v1/dashboards", { data: body });
  if (!res.ok()) return { id: "", status: res.status() };
  return { id: (await res.json()).id, status: res.status() };
}

test.describe("Dashboards × RBAC", () => {
  test.describe.configure({ mode: "serial" });
  const stamp = Date.now().toString(36);
  const cleanup: { dashboards: string[]; groups: string[]; integrations: string[]; sas: string[] } = {
    dashboards: [],
    groups: [],
    integrations: [],
    sas: [],
  };

  test.afterAll(async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    await logIn(page);
    const admin = page.request;
    for (const id of cleanup.dashboards) await admin.delete(`/api/v1/dashboards/${id}`);
    for (const id of cleanup.integrations) await admin.delete(`/api/v1/integrations/${id}`);
    for (const id of cleanup.groups) await admin.delete(`/api/v1/settings/groups/${id}`);
    for (const id of cleanup.sas) await admin.delete(`/api/v1/settings/service-accounts/${id}`);
    await page.context().close();
  });

  test("team dashboard is invisible to non-members; org-wide is visible but read-only for viewers", async ({ page, browser }) => {
    await logIn(page);
    const admin = page.request;
    await ensureUser(admin, VIEWER_EMAIL, "E2E Dash Viewer", VIEWER_PASSWORD);
    const teamB = await makeGroup(admin, `e2e-dash-teamb-${stamp}`, "E2E Dash TeamB");
    cleanup.groups.push(teamB);

    const orgDash = await makeDashboard(admin, { name: `E2E OrgWide ${stamp}` });
    expect(orgDash.status).toBe(201);
    cleanup.dashboards.push(orgDash.id);
    const teamDash = await makeDashboard(admin, { name: `E2E TeamB Board ${stamp}`, groupId: teamB });
    expect(teamDash.status).toBe(201);
    cleanup.dashboards.push(teamDash.id);

    const viewer = await userPage(browser, VIEWER_EMAIL, VIEWER_PASSWORD);
    const seen = (await (await viewer.request.get("/api/v1/dashboards")).json()).dashboards ?? [];
    const ids = seen.map((d: { id: string }) => d.id);
    expect(ids).toContain(orgDash.id);
    expect(ids).not.toContain(teamDash.id);

    // Direct fetch of the invisible team dashboard reads as nonexistent.
    expect((await viewer.request.get(`/api/v1/dashboards/${teamDash.id}`)).status()).toBe(404);

    // The visible org-wide dashboard is read-only for a viewer: the row
    // says so, and the write is refused at the gate.
    const orgRow = seen.find((d: { id: string }) => d.id === orgDash.id);
    expect(orgRow.canManage).toBe(false);
    const edit = await viewer.request.put(`/api/v1/dashboards/${orgDash.id}`, {
      data: { name: "hijacked", isDefault: false, autoIncludeAll: true, defaultWidgetType: "traffic_sparkline", position: 0, items: [] },
    });
    expect(edit.status()).toBe(403);
    expect((await viewer.request.delete(`/api/v1/dashboards/${orgDash.id}`)).status()).toBe(403);
    await viewer.context().close();
  });

  test("team editor manages exactly their team's dashboards (EE)", async ({ page, browser }) => {
    await logIn(page);
    const admin = page.request;
    const lic = await (await admin.get("/api/v1/license")).json();
    test.skip(!lic?.features?.rbac_advanced, "cell has no rbac_advanced entitlement");

    const uid = await ensureUser(admin, EDITOR_EMAIL, "E2E Dash TeamEditor", EDITOR_PASSWORD);
    const teamA = await makeGroup(admin, `e2e-dash-teama-${stamp}`, "E2E Dash TeamA");
    cleanup.groups.push(teamA);
    await admin.post(`/api/v1/settings/groups/${teamA}/members`, { data: { user_id: uid, role: "editor" } });
    const teamB = await makeGroup(admin, `e2e-dash-teamb2-${stamp}`, "E2E Dash TeamB2");
    cleanup.groups.push(teamB);
    const otherDash = await makeDashboard(admin, { name: `E2E TeamB2 Board ${stamp}`, groupId: teamB });
    cleanup.dashboards.push(otherDash.id);

    const editor = await userPage(browser, EDITOR_EMAIL, EDITOR_PASSWORD);
    // Org-wide creation needs an org-wide editor role — refused.
    expect((await makeDashboard(editor.request, { name: `E2E Nope ${stamp}` })).status).toBe(403);
    // Someone else's team — refused (unknown team from their view? no:
    // the team exists; membership is what's missing).
    expect((await makeDashboard(editor.request, { name: `E2E Nope2 ${stamp}`, groupId: teamB })).status).toBe(403);

    // Their own team: create, update, delete — full lifecycle.
    const mine = await makeDashboard(editor.request, { name: `E2E TeamA Board ${stamp}`, groupId: teamA });
    expect(mine.status).toBe(201);
    const upd = await editor.request.put(`/api/v1/dashboards/${mine.id}`, {
      data: { name: `E2E TeamA Board v2 ${stamp}`, isDefault: false, autoIncludeAll: true, defaultWidgetType: "traffic_sparkline", position: 0, items: [] },
    });
    expect(upd.ok()).toBeTruthy();
    // The other team's dashboard stays invisible + unmanageable.
    expect((await editor.request.get(`/api/v1/dashboards/${otherDash.id}`)).status()).toBe(404);
    expect((await editor.request.delete(`/api/v1/dashboards/${otherDash.id}`)).status()).toBe(404);
    expect((await editor.request.delete(`/api/v1/dashboards/${mine.id}`)).status()).toBe(204);
    await editor.context().close();
  });

  test("widget data never leaks an out-of-scope integration — API and Health page", async ({ page, browser }) => {
    await logIn(page);
    const admin = page.request;
    // Two integrations over real catalog services; the viewer is granted
    // only the first via group attach.
    const catalog = (await (await admin.get("/api/v1/services?range=30d")).json()).services ?? [];
    test.skip(catalog.length < 2, "need at least two catalog services");
    const [svcA, svcB] = catalog.map((s: { service_name: string }) => s.service_name);

    const mkA = await admin.post("/api/v1/integrations", {
      data: { slug: `e2e-dash-granted-${stamp}`, name: `E2E DashGranted ${stamp}`, matchers: [{ operator: "equals", value: svcA }] },
    });
    const integA = (await mkA.json()).integration.id;
    cleanup.integrations.push(integA);
    const mkB = await admin.post("/api/v1/integrations", {
      data: { slug: `e2e-dash-hidden-${stamp}`, name: `E2E DashHidden ${stamp}`, matchers: [{ operator: "equals", value: svcB }] },
    });
    const integB = (await mkB.json()).integration.id;
    cleanup.integrations.push(integB);

    const uid = await ensureUser(admin, VIEWER_EMAIL, "E2E Dash Viewer", VIEWER_PASSWORD);
    const grant = await makeGroup(admin, `e2e-dash-grant-${stamp}`, "E2E Dash Grant");
    cleanup.groups.push(grant);
    await admin.post(`/api/v1/settings/groups/${grant}/members`, { data: { user_id: uid, role: "viewer" } });
    expect((await admin.put(`/api/v1/integrations/${integA}/groups`, { data: { group_ids: [grant] } })).ok()).toBeTruthy();

    // An org-wide dashboard whose ITEMS reference both integrations.
    const dash = await makeDashboard(admin, {
      name: `E2E Widget Board ${stamp}`,
      items: [
        { entityKind: "integration", integrationId: integA, widgetType: "error_count", position: 0 },
        { entityKind: "integration", integrationId: integB, widgetType: "error_count", position: 1 },
      ],
    });
    expect(dash.status).toBe(201);
    cleanup.dashboards.push(dash.id);

    const viewer = await userPage(browser, VIEWER_EMAIL, VIEWER_PASSWORD);
    // The dashboard itself is org-wide → visible. But the widget DATA
    // source (/integrations) is filtered: only the granted integration
    // comes back, so only its widget can render.
    const integs = (await (await viewer.request.get("/api/v1/integrations?range=30d")).json()).integrations ?? [];
    const names = integs.map((i: { id: string }) => i.id);
    expect(names).toContain(integA);
    expect(names).not.toContain(integB);
    // Direct data probes for the hidden integration read as nonexistent.
    expect((await viewer.request.get(`/api/v1/integrations/${integB}`)).status()).toBe(404);

    // And the rendered Health page shows exactly the granted card.
    await viewer.goto("/health");
    await expect(viewer.getByText(`E2E DashGranted ${stamp}`).first()).toBeVisible({ timeout: 15_000 });
    await expect(viewer.getByText(`E2E DashHidden ${stamp}`)).toHaveCount(0);
    await viewer.context().close();
  });

  test("scoped service account sees team dashboards only via membership", async ({ page, request }) => {
    await logIn(page);
    const admin = page.request;

    const team = await makeGroup(admin, `e2e-dash-sateam-${stamp}`, "E2E Dash SATeam");
    cleanup.groups.push(team);
    const teamDash = await makeDashboard(admin, { name: `E2E SATeam Board ${stamp}`, groupId: team });
    cleanup.dashboards.push(teamDash.id);
    const orgDash = await makeDashboard(admin, { name: `E2E SAOrg Board ${stamp}` });
    cleanup.dashboards.push(orgDash.id);

    const sa = await (
      await admin.post("/api/v1/settings/service-accounts", {
        data: { name: `e2e-dash-sa-${stamp}`, role: "viewer" },
      })
    ).json();
    cleanup.sas.push(sa.id);
    const token = (
      await (await admin.post(`/api/v1/settings/service-accounts/${sa.id}/tokens`, { data: { name: "t1" } })).json()
    ).plaintext;
    const auth = { Authorization: `Bearer ${token}` };

    // Group-less scoped SA: org-wide dashboards only.
    let seen = (await (await request.get("/api/v1/dashboards", { headers: auth })).json()).dashboards ?? [];
    expect(seen.map((d: { id: string }) => d.id)).toContain(orgDash.id);
    expect(seen.map((d: { id: string }) => d.id)).not.toContain(teamDash.id);

    // Joining the team reveals the team dashboard — same rule as users.
    await admin.post(`/api/v1/settings/groups/${team}/members`, { data: { service_account_id: sa.id, role: "viewer" } });
    seen = (await (await request.get("/api/v1/dashboards", { headers: auth })).json()).dashboards ?? [];
    expect(seen.map((d: { id: string }) => d.id)).toContain(teamDash.id);
  });
});
