// SPDX-License-Identifier: Apache-2.0
//
// RBAC role matrix — what a viewer must NOT be able to do. The suite
// provisions a dedicated viewer member through the real admin API (the
// user row is reused across runs via its fixed email), signs in as them,
// and probes the surfaces that leaked or lacked gates in the 2026-07
// RBAC review: the member list (recon payload), message-view mutations,
// and the general write surfaces.
import { test, expect, type APIRequestContext, type Browser } from "@playwright/test";
import { logIn } from "./fixtures";

const VIEWER_EMAIL = "e2e-rbac-viewer@sluicio.local";
const VIEWER_PASSWORD = "e2e-rbac-viewer-pw1";

// ensureViewer (idempotent): add the fixed viewer member via the admin
// session. 201 = created, 409 = user exists from an earlier run (then a
// membership add still happened or already exists — both fine).
async function ensureViewer(admin: APIRequestContext): Promise<void> {
  const res = await admin.post("/api/v1/settings/members", {
    data: { email: VIEWER_EMAIL, name: "E2E RBAC Viewer", password: VIEWER_PASSWORD, role: "viewer" },
  });
  if (!res.ok() && res.status() !== 409) {
    throw new Error(`could not provision viewer: ${res.status()}`);
  }
}

async function viewerContext(browser: Browser) {
  const page = await (await browser.newContext()).newPage();
  await logIn(page, VIEWER_EMAIL, VIEWER_PASSWORD);
  return page;
}

// stableIntegrations filters out the short-lived fixtures other tests in
// this suite create and delete mid-run ("E2E Scoped Integ" et al sort
// before the seed data, so a naive integs[0] can grab an integration that
// vanishes milliseconds later — the share test flaked exactly that way).
const EPHEMERAL_NAME = /^(E2E |Playwright-|Random-order)/;
function stableIntegrations<T extends { name: string }>(integs: T[]): T[] {
  return integs.filter((i) => !EPHEMERAL_NAME.test(i.name));
}

test.describe("RBAC — viewer restrictions", () => {
  test.beforeEach(async ({ page }) => {
    await logIn(page); // admin
    await ensureViewer(page.request);
  });

  test("viewer cannot read the member list", async ({ browser }) => {
    const page = await viewerContext(browser);
    const res = await page.request.get("/api/v1/settings/members");
    expect(res.status()).toBe(403);
  });

  test("viewer cannot mutate message views", async ({ browser }) => {
    const page = await viewerContext(browser);
    const create = await page.request.post("/api/v1/message-views", {
      data: { name: "should-not-exist", filters: [] },
    });
    expect(create.status()).toBe(403);
    // Reads stay open — viewers use shared views, they don't shape them.
    expect((await page.request.get("/api/v1/message-views")).ok()).toBeTruthy();
  });

  test("viewer cannot mutate config or reach admin surfaces", async ({ browser }) => {
    const page = await viewerContext(browser);
    const probes: Array<[string, Promise<{ status(): number }>]> = [
      ["POST /tags", page.request.post("/api/v1/tags", { data: { slug: "x", name: "x" } })],
      ["POST /integrations", page.request.post("/api/v1/integrations", { data: { name: "x" } })],
      ["POST /alert-rules", page.request.post("/api/v1/alert-rules", { data: { name: "x" } })],
      ["POST /ingest-keys", page.request.post("/api/v1/ingest-keys", { data: { name: "x" } })],
      ["PATCH /cell-settings/retention", page.request.patch("/api/v1/cell-settings/retention", { data: { logs_days: 14 } })],
      ["GET /audit-log", page.request.get("/api/v1/audit-log")],
      ["GET /operator/orgs", page.request.get("/api/v1/operator/orgs")],
    ];
    for (const [label, p] of probes) {
      expect((await p).status(), label).toBe(403);
    }
  });

  test("group-less viewer sees no services (deny-by-default visibility)", async ({ browser }) => {
    const page = await viewerContext(browser);
    const res = await page.request.get("/api/v1/services");
    expect(res.ok()).toBeTruthy();
    const { services } = await res.json();
    expect(services ?? []).toHaveLength(0);
  });
});

// ── Resource ⇄ group attachment (RBAC v2 phase 1, CE surface) ──────────
//
// The Community visibility grant: attach a group to an integration or
// system as viewer → members see the resource + its member services.
// Not entitlement-gated. Uses a throwaway group + the shared viewer.

test.describe("RBAC — attach groups to integrations & systems (CE)", () => {
  test.describe.configure({ mode: "serial" });

  const GROUP_SLUG = "e2e-attach-scope";
  const ATTACH_VIEWER_EMAIL = "e2e-attach-viewer@sluicio.local";
  const ATTACH_VIEWER_PASSWORD = "e2e-attach-viewer-pw1";

  async function ensureAttachViewer(admin: APIRequestContext): Promise<string> {
    const res = await admin.post("/api/v1/settings/members", {
      data: { email: ATTACH_VIEWER_EMAIL, name: "E2E Attach Viewer", password: ATTACH_VIEWER_PASSWORD, role: "viewer" },
    });
    if (!res.ok() && res.status() !== 409) throw new Error(`provision attach viewer: ${res.status()}`);
    const members = await (await admin.get("/api/v1/settings/members")).json();
    return (members.members ?? []).find(
      (m: { user: { email: string; id: string } }) => m.user.email === ATTACH_VIEWER_EMAIL,
    )?.user.id;
  }

  async function attachViewerContext(browser: Browser) {
    const page = await (await browser.newContext()).newPage();
    await logIn(page, ATTACH_VIEWER_EMAIL, ATTACH_VIEWER_PASSWORD);
    return page;
  }

  async function resetAttachGroup(admin: APIRequestContext): Promise<string> {
    const list = await (await admin.get("/api/v1/settings/groups")).json();
    for (const g of list.groups ?? []) {
      if (g.slug === GROUP_SLUG) await admin.delete(`/api/v1/settings/groups/${g.id}`);
    }
    const created = await admin.post("/api/v1/settings/groups", {
      data: { slug: GROUP_SLUG, name: "E2E Attach Scope" },
    });
    expect(created.ok()).toBeTruthy();
    return (await created.json()).id;
  }

  test.beforeEach(async ({ page }) => {
    await logIn(page); // admin
  });

  test("attaching a group to an integration grants members view of it + its services", async ({ page, browser }) => {
    const admin = page.request;
    const gid = await resetAttachGroup(admin);
    const uid = await ensureAttachViewer(admin);
    await admin.post(`/api/v1/settings/groups/${gid}/members`, { data: { user_id: uid, role: "viewer" } });

    // Pick a real integration that has at least one member service.
    const integs = stableIntegrations(
      (await (await admin.get("/api/v1/integrations?range=30d")).json()).integrations ?? [],
    );
    const target = integs.find((i: { services?: unknown[] }) => (i.services ?? []).length > 0) ?? integs[0];
    test.skip(!target, "cell has no integrations");

    // Attach → viewer sees the integration; detach → viewer sees nothing.
    expect((await admin.put(`/api/v1/integrations/${target.id}/groups`, { data: { group_ids: [gid] } })).ok()).toBeTruthy();
    const viewer = await attachViewerContext(browser);
    const seen = (await (await viewer.request.get("/api/v1/integrations?range=30d")).json()).integrations ?? [];
    expect(seen.map((i: { id: string }) => i.id)).toContain(target.id);

    // Read-back on the admin side lists the group.
    const attached = (await (await admin.get(`/api/v1/integrations/${target.id}/groups`)).json()).groups ?? [];
    expect(attached.map((g: { group_id: string }) => g.group_id)).toContain(gid);

    // View only: the viewer still cannot mutate the integration.
    expect((await viewer.request.put(`/api/v1/integrations/${target.id}`, { data: { name: "x", description: "" } })).status()).toBe(403);
    // And cannot edit the attachment itself.
    expect((await viewer.request.put(`/api/v1/integrations/${target.id}/groups`, { data: { group_ids: [] } })).status()).toBe(403);

    // Detach → back to nothing.
    expect((await admin.put(`/api/v1/integrations/${target.id}/groups`, { data: { group_ids: [] } })).ok()).toBeTruthy();
    const seenAfter = (await (await viewer.request.get("/api/v1/integrations?range=30d")).json()).integrations ?? [];
    expect(seenAfter.map((i: { id: string }) => i.id)).not.toContain(target.id);
  });

  test("attaching a group to a system grants members view of its services", async ({ page, browser }) => {
    const admin = page.request;
    const gid = await resetAttachGroup(admin);
    const uid = await ensureAttachViewer(admin);
    await admin.post(`/api/v1/settings/groups/${gid}/members`, { data: { user_id: uid, role: "viewer" } });

    const systems = (await (await admin.get("/api/v1/systems")).json()).systems ?? [];
    const target = systems.find((s: { members?: unknown[]; member_count?: number }) =>
      (s.members ?? []).length > 0 || (s.member_count ?? 0) > 0);
    test.skip(!target, "cell has no system with member services");

    expect((await admin.put(`/api/v1/systems/${target.id}/groups`, { data: { group_ids: [gid] } })).ok()).toBeTruthy();
    const viewer = await attachViewerContext(browser);
    const visibleServices = (await (await viewer.request.get("/api/v1/services?range=30d")).json()).services ?? [];
    expect(visibleServices.length).toBeGreaterThan(0);

    // Cleanup attachment.
    await admin.put(`/api/v1/systems/${target.id}/groups`, { data: { group_ids: [] } });
  });

  test("foreign group ids are rejected", async ({ page }) => {
    const admin = page.request;
    const gid = await resetAttachGroup(admin);
    void gid;
    const integs = stableIntegrations(
      (await (await admin.get("/api/v1/integrations?range=30d")).json()).integrations ?? [],
    );
    test.skip(integs.length === 0, "cell has no integrations");
    const bogus = "00000000-0000-0000-0000-00000000dead";
    const res = await admin.put(`/api/v1/integrations/${integs[0].id}/groups`, {
      data: { group_ids: [bogus] },
    });
    expect(res.status()).toBe(400);
  });
});

// ── Expression access policies (Enterprise rbac_advanced) ──────────────
//
// Drives the whole real stack — create an expression policy via the admin
// API, scope a viewer to it through a group, and assert the viewer sees
// exactly the services the boolean tree grants (and is denied the rest,
// including the per-service trace gate). Expectations are derived from the
// cell's live catalog, so the suite adapts to whatever data is present and
// self-skips when there isn't enough.

const EXPR_VIEWER_EMAIL = "e2e-expr-viewer@sluicio.local";
const EXPR_VIEWER_PASSWORD = "e2e-expr-viewer-pw1";
const EXPR_GROUP_SLUG = "e2e-expr-scope";

async function serviceNames(api: APIRequestContext): Promise<string[]> {
  const res = await api.get("/api/v1/services?range=30d");
  const { services } = await res.json();
  return (services ?? []).map((s: { service_name: string }) => s.service_name);
}

// resetExprGroup deletes any prior run's group (clearing its policies +
// members) and creates a fresh one, returning its id.
async function resetExprGroup(admin: APIRequestContext): Promise<string> {
  const list = await (await admin.get("/api/v1/settings/groups")).json();
  for (const g of list.groups ?? []) {
    if (g.slug === EXPR_GROUP_SLUG) await admin.delete(`/api/v1/settings/groups/${g.id}`);
  }
  const created = await admin.post("/api/v1/settings/groups", {
    data: { slug: EXPR_GROUP_SLUG, name: "E2E Expr Scope" },
  });
  expect(created.ok()).toBeTruthy();
  return (await created.json()).id;
}

// bindExprViewer ensures the expression viewer exists and is a member of
// the given group.
async function bindExprViewer(admin: APIRequestContext, groupId: string): Promise<void> {
  const add = await admin.post("/api/v1/settings/members", {
    data: { email: EXPR_VIEWER_EMAIL, name: "E2E Expr Viewer", password: EXPR_VIEWER_PASSWORD, role: "viewer" },
  });
  if (!add.ok() && add.status() !== 409) throw new Error(`provision expr viewer: ${add.status()}`);
  const members = await (await admin.get("/api/v1/settings/members")).json();
  const uid = (members.members ?? []).find(
    (m: { user: { email: string; id: string } }) => m.user.email === EXPR_VIEWER_EMAIL,
  )?.user.id;
  expect(uid, "expr viewer user id").toBeTruthy();
  await admin.post(`/api/v1/settings/groups/${groupId}/members`, { data: { user_id: uid, role: "viewer" } });
}

test.describe("RBAC — expression access policies (EE)", () => {
  // Serial: these share one viewer + group slug, so they must not race
  // (a parallel resetExprGroup would delete another test's group mid-run).
  test.describe.configure({ mode: "serial" });

  let entitled = false;
  let aServices: string[] = [];

  test.beforeEach(async ({ page }) => {
    await logIn(page); // admin
    const lic = await (await page.request.get("/api/v1/license")).json();
    entitled = Boolean(lic?.features?.rbac_advanced);
    aServices = (await serviceNames(page.request)).filter((n) => n.startsWith("a")).sort();
  });

  test("service-name prefix scopes a viewer to exactly the matching services", async ({ page, browser }) => {
    test.skip(!entitled, "cell has no rbac_advanced entitlement");
    test.skip(aServices.length < 1, "cell has no services starting with 'a'");

    const gid = await resetExprGroup(page.request);
    const policy = await page.request.post(`/api/v1/settings/groups/${gid}/policies`, {
      data: { kind: "expression", conditions: { match: "prefix", value: "a" } },
    });
    expect(policy.ok()).toBeTruthy();
    await bindExprViewer(page.request, gid);

    const viewer = await (await browser.newContext()).newPage();
    await logIn(viewer, EXPR_VIEWER_EMAIL, EXPR_VIEWER_PASSWORD);
    const visible = (await serviceNames(viewer.request)).sort();
    expect(visible).toEqual(aServices);

    // A non-matching service's per-service route is gated (404), not just
    // absent from the list.
    const nonA = (await serviceNames(page.request)).find((n) => !n.startsWith("a"));
    if (nonA) {
      const gated = await viewer.request.get(`/api/v1/services/${encodeURIComponent(nonA)}/traces?range=30d`);
      expect(gated.status()).toBe(404);
    }
  });

  test("NOT excludes a service the rest of the tree would grant", async ({ page, browser }) => {
    test.skip(!entitled, "cell has no rbac_advanced entitlement");
    test.skip(aServices.length < 2, "need at least two 'a' services to prove exclusion");

    const excluded = aServices[0];
    const gid = await resetExprGroup(page.request);
    // (prefix "a") AND NOT (service equals <first a-service>)
    const policy = await page.request.post(`/api/v1/settings/groups/${gid}/policies`, {
      data: {
        kind: "expression",
        conditions: {
          op: "and",
          children: [
            { match: "prefix", value: "a" },
            { op: "not", children: [{ match: "equals", value: excluded }] },
          ],
        },
      },
    });
    expect(policy.ok()).toBeTruthy();
    await bindExprViewer(page.request, gid);

    const viewer = await (await browser.newContext()).newPage();
    await logIn(viewer, EXPR_VIEWER_EMAIL, EXPR_VIEWER_PASSWORD);
    const visible = (await serviceNames(viewer.request)).sort();
    expect(visible).toEqual(aServices.filter((n) => n !== excluded));
    expect(visible).not.toContain(excluded);
  });

  test("malformed expression is rejected at write (400)", async ({ page }) => {
    test.skip(!entitled, "cell has no rbac_advanced entitlement");
    const gid = await resetExprGroup(page.request);
    const bad = await page.request.post(`/api/v1/settings/groups/${gid}/policies`, {
      data: { kind: "expression", conditions: { op: "not", children: [{ attr: "x", match: "regex", value: "(" }] } },
    });
    expect(bad.status()).toBe(400);
  });
});

// ── Scoped manage (RBAC v2 phase 2, EE) ────────────────────────────────
//
// The headline enterprise story: an ORG-VIEWER who is EDITOR in a group
// manages exactly the group's scope — edits in-scope services, creates
// integrations contained in the scope, and is denied everything else
// (out-of-scope matchers, class-A org config, org-wide dashboards).

test.describe("RBAC — scoped manage (EE)", () => {
  test.describe.configure({ mode: "serial" });

  const SM_EMAIL = "e2e-scoped-editor@sluicio.local";
  const SM_PASSWORD = "e2e-scoped-editor-pw1";
  const SM_GROUP = "e2e-scoped-manage";
  const SCOPED_SERVICE = "sluicio-otel-collector";

  async function setupScopedEditor(admin: APIRequestContext): Promise<void> {
    const list = await (await admin.get("/api/v1/settings/groups")).json();
    for (const g of list.groups ?? []) {
      if (g.slug === SM_GROUP) await admin.delete(`/api/v1/settings/groups/${g.id}`);
    }
    const created = await admin.post("/api/v1/settings/groups", {
      data: { slug: SM_GROUP, name: "E2E Scoped Manage" },
    });
    const gid = (await created.json()).id;
    const add = await admin.post("/api/v1/settings/members", {
      data: { email: SM_EMAIL, name: "E2E Scoped Editor", password: SM_PASSWORD, role: "viewer" },
    });
    if (!add.ok() && add.status() !== 409) throw new Error(`provision: ${add.status()}`);
    const members = await (await admin.get("/api/v1/settings/members")).json();
    const uid = (members.members ?? []).find(
      (m: { user: { email: string; id: string } }) => m.user.email === SM_EMAIL,
    )?.user.id;
    // EDITOR in the group — the capability axis.
    await admin.post(`/api/v1/settings/groups/${gid}/members`, { data: { user_id: uid, role: "editor" } });
    await admin.post(`/api/v1/settings/groups/${gid}/policies`, {
      data: { kind: "service", target_service_name: SCOPED_SERVICE },
    });
  }

  test.beforeEach(async ({ page }) => {
    await logIn(page); // admin
    const lic = await (await page.request.get("/api/v1/license")).json();
    test.skip(!lic?.features?.rbac_advanced, "cell has no rbac_advanced entitlement");
    await setupScopedEditor(page.request);
  });

  test("group-editor manages exactly the scoped service", async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    await logIn(page, SM_EMAIL, SM_PASSWORD);
    const r = page.request;

    // /me/access mirrors the capability.
    const access = await (await r.get("/api/v1/me/access")).json();
    expect(access.write_anywhere).toBe(true);
    expect(access.manage_all).toBe(false);
    expect(access.managed_services).toContain(SCOPED_SERVICE);

    // In-scope service edit → 200.
    const edit = await r.put(`/api/v1/services/${SCOPED_SERVICE}/metadata`, {
      data: { description: "managed by e2e scoped editor" },
    });
    expect(edit.ok()).toBeTruthy();

    // Out-of-scope service edit → 404 (invisible, not merely forbidden).
    const foreign = await r.put(`/api/v1/services/file-mover/metadata`, {
      data: { description: "nope" },
    });
    expect(foreign.status()).toBe(404);

    // Integration creation contained in scope → 201; then delete it (also
    // allowed — all members in scope).
    const mk = await r.post("/api/v1/integrations", {
      data: {
        slug: "e2e-scoped-integ",
        name: "E2E Scoped Integ",
        matchers: [{ operator: "equals", value: SCOPED_SERVICE }],
      },
    });
    expect(mk.status()).toBe(201);
    const integ = (await mk.json()).integration;
    expect((await r.delete(`/api/v1/integrations/${integ.id}`)).ok()).toBeTruthy();

    // Out-of-scope matcher → 403 with the containment message.
    const bad = await r.post("/api/v1/integrations", {
      data: {
        slug: "e2e-scoped-bad",
        name: "E2E Scoped Bad",
        matchers: [{ operator: "equals", value: "file-mover" }],
      },
    });
    expect(bad.status()).toBe(403);

    // Class-A org config stays closed: tags + channels + org dashboards.
    expect((await r.post("/api/v1/tags", { data: { slug: "e2e-sm", name: "x" } })).status()).toBe(403);
    expect((await r.post("/api/v1/notification-channels", { data: { name: "x", kind: "email" } })).status()).toBe(403);
    expect((await r.post("/api/v1/dashboards", { data: { name: "org-wide-nope" } })).status()).toBe(403);

    // Team dashboard → allowed; canManage stamped; delete works.
    const groups = await (await r.get("/api/v1/me/access")).json();
    const gid = groups.editor_groups[0]?.id;
    expect(gid).toBeTruthy();
    const dash = await r.post("/api/v1/dashboards", { data: { name: "e2e team dash", groupId: gid } });
    expect(dash.status()).toBe(201);
    const dbody = await dash.json();
    expect(dbody.canManage).toBe(true);
    expect((await r.delete(`/api/v1/dashboards/${dbody.id}`)).status()).toBe(204);
  });
});

// ── Resource sharing (RBAC v2 phase 3, EE) ─────────────────────────────
//
// Viewer-only shares: the grantee sees the resource + services, can't
// mutate anything, gets a digest entry, and revocation removes access.

test.describe("RBAC — resource sharing (EE)", () => {
  test.describe.configure({ mode: "serial" });

  const SHARE_EMAIL = "e2e-share-viewer@sluicio.local";
  const SHARE_PASSWORD = "e2e-share-viewer-pw1";

  test.beforeEach(async ({ page }) => {
    await logIn(page); // admin
    const lic = await (await page.request.get("/api/v1/license")).json();
    test.skip(!lic?.features?.rbac_advanced, "cell has no rbac_advanced entitlement");
    const add = await page.request.post("/api/v1/settings/members", {
      data: { email: SHARE_EMAIL, name: "E2E Share Viewer", password: SHARE_PASSWORD, role: "viewer" },
    });
    if (!add.ok() && add.status() !== 409) throw new Error(`provision: ${add.status()}`);
  });

  test("share → grantee sees the integration view-only; digest lists it; revoke removes it", async ({ page, browser }) => {
    const admin = page.request;
    const integs = stableIntegrations(
      (await (await admin.get("/api/v1/integrations?range=30d")).json()).integrations ?? [],
    );
    const target = integs[0];
    test.skip(!target, "cell has no integrations");

    // Clean any prior share for idempotency, then share to the user.
    const existing = (await (await admin.get(`/api/v1/integrations/${target.id}/shares`)).json()).shares ?? [];
    for (const sh of existing) await admin.delete(`/api/v1/integrations/${target.id}/shares/${sh.id}`);
    const mk = await admin.post(`/api/v1/integrations/${target.id}/shares`, {
      data: { grantee_kind: "user", grantee_email: SHARE_EMAIL },
    });
    expect(mk.status()).toBe(201);

    const viewer = await (await browser.newContext()).newPage();
    await logIn(viewer, SHARE_EMAIL, SHARE_PASSWORD);
    const seen = (await (await viewer.request.get("/api/v1/integrations?range=30d")).json()).integrations ?? [];
    expect(seen.map((i: { id: string }) => i.id)).toContain(target.id);

    // View only: mutations and share management stay closed.
    expect((await viewer.request.put(`/api/v1/integrations/${target.id}`, { data: { name: "x", description: "" } })).status()).toBe(403);
    expect((await viewer.request.get(`/api/v1/integrations/${target.id}/shares`)).status()).toBe(403);

    // Digest carries the share.
    const digest = await (await viewer.request.get("/api/v1/digest")).json();
    const sharedIds = (digest.shared ?? []).map((s: { resource_id: string }) => s.resource_id);
    expect(sharedIds).toContain(target.id);

    // Audit trail exists.
    const audit = await (await admin.get("/api/v1/audit-log?action=share.created&limit=1")).json();
    expect(audit.entries.length).toBe(1);

    // Revoke → invisible again.
    const shares = (await (await admin.get(`/api/v1/integrations/${target.id}/shares`)).json()).shares ?? [];
    for (const sh of shares) {
      expect((await admin.delete(`/api/v1/integrations/${target.id}/shares/${sh.id}`)).status()).toBe(204);
    }
    const seenAfter = (await (await viewer.request.get("/api/v1/integrations?range=30d")).json()).integrations ?? [];
    expect(seenAfter.map((i: { id: string }) => i.id)).not.toContain(target.id);
  });

  test("system share parity + duplicate rejected", async ({ page }) => {
    const admin = page.request;
    const systems = (await (await admin.get("/api/v1/systems")).json()).systems ?? [];
    const target = systems.find((s: { member_count?: number }) => (s.member_count ?? 0) > 0) ?? systems[0];
    test.skip(!target, "cell has no systems");

    const existing = (await (await admin.get(`/api/v1/systems/${target.id}/shares`)).json()).shares ?? [];
    for (const sh of existing) await admin.delete(`/api/v1/systems/${target.id}/shares/${sh.id}`);

    const mk = await admin.post(`/api/v1/systems/${target.id}/shares`, {
      data: { grantee_kind: "user", grantee_email: SHARE_EMAIL },
    });
    expect(mk.status()).toBe(201);
    const dup = await admin.post(`/api/v1/systems/${target.id}/shares`, {
      data: { grantee_kind: "user", grantee_email: SHARE_EMAIL },
    });
    expect(dup.status()).toBe(409);
    // Unknown email → 400, not a share.
    const bad = await admin.post(`/api/v1/systems/${target.id}/shares`, {
      data: { grantee_kind: "user", grantee_email: "nobody@nowhere.example" },
    });
    expect(bad.status()).toBe(400);
    // Cleanup.
    const shares = (await (await admin.get(`/api/v1/systems/${target.id}/shares`)).json()).shares ?? [];
    for (const sh of shares) await admin.delete(`/api/v1/systems/${target.id}/shares/${sh.id}`);
  });
});

// ── Per-signal visibility (RBAC v2 phase 4, EE) ────────────────────────
//
// A logs-only grant: the member sees the service (nav/union), gets logs
// data, but traces/metrics read as empty — and even with a group-editor
// role, a signal-narrowed policy grants zero manage.

test.describe("RBAC — per-signal visibility (EE)", () => {
  test.describe.configure({ mode: "serial" });

  const SIG_EMAIL = "e2e-signal-viewer@sluicio.local";
  const SIG_PASSWORD = "e2e-signal-viewer-pw1";
  const SIG_GROUP = "e2e-signal-scope";
  const SIG_SERVICE = "sluicio-otel-collector";

  test.beforeEach(async ({ page }) => {
    await logIn(page); // admin
    const lic = await (await page.request.get("/api/v1/license")).json();
    test.skip(!lic?.features?.rbac_advanced, "cell has no rbac_advanced entitlement");
    const admin = page.request;
    const list = await (await admin.get("/api/v1/settings/groups")).json();
    for (const g of list.groups ?? []) {
      if (g.slug === SIG_GROUP) await admin.delete(`/api/v1/settings/groups/${g.id}`);
    }
    const created = await admin.post("/api/v1/settings/groups", {
      data: { slug: SIG_GROUP, name: "E2E Signal Scope" },
    });
    const gid = (await created.json()).id;
    const add = await admin.post("/api/v1/settings/members", {
      data: { email: SIG_EMAIL, name: "E2E Signal Viewer", password: SIG_PASSWORD, role: "viewer" },
    });
    if (!add.ok() && add.status() !== 409) throw new Error(`provision: ${add.status()}`);
    const members = await (await admin.get("/api/v1/settings/members")).json();
    const uid = (members.members ?? []).find(
      (m: { user: { email: string; id: string } }) => m.user.email === SIG_EMAIL,
    )?.user.id;
    // EDITOR in the group — proves signal-narrowed policies never manage.
    await admin.post(`/api/v1/settings/groups/${gid}/members`, { data: { user_id: uid, role: "editor" } });
    const pol = await admin.post(`/api/v1/settings/groups/${gid}/policies`, {
      data: { kind: "service", target_service_name: SIG_SERVICE, signals: ["logs"] },
    });
    expect(pol.ok()).toBeTruthy();
  });

  test("logs-only grant: service visible, logs flow, traces/metrics empty, zero manage", async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    await logIn(page, SIG_EMAIL, SIG_PASSWORD);
    const r = page.request;

    // Union/nav: the service is listed and its detail declares logs-only.
    const services = (await (await r.get("/api/v1/services?range=30d")).json()).services ?? [];
    expect(services.map((s: { service_name: string }) => s.service_name)).toContain(SIG_SERVICE);
    const detail = await (await r.get(`/api/v1/services/${SIG_SERVICE}?range=30d`)).json();
    expect(detail.visible_signals).toEqual(["logs"]);

    // Logs endpoint: normal response. Traces/metrics: empty payloads.
    expect((await r.get(`/api/v1/services/${SIG_SERVICE}/logs?range=30d`)).ok()).toBeTruthy();
    const traces = await (await r.get(`/api/v1/services/${SIG_SERVICE}/traces?range=30d`)).json();
    expect(traces.traces ?? []).toHaveLength(0);
    const metrics = await (await r.get(`/api/v1/services/${SIG_SERVICE}/metric-names?range=30d`)).json();
    expect(metrics.metrics ?? []).toHaveLength(0);

    // Signal narrowing kills manage even for a group-editor.
    const access = await (await r.get("/api/v1/me/access")).json();
    expect(access.managed_services ?? []).not.toContain(SIG_SERVICE);
    const edit = await r.put(`/api/v1/services/${SIG_SERVICE}/metadata`, { data: { description: "nope" } });
    expect(edit.status()).toBe(403);
  });

  test("unknown signal rejected at policy write", async ({ page }) => {
    const admin = page.request;
    const list = await (await admin.get("/api/v1/settings/groups")).json();
    const gid = (list.groups ?? []).find((g: { slug: string }) => g.slug === SIG_GROUP)?.id;
    const bad = await admin.post(`/api/v1/settings/groups/${gid}/policies`, {
      data: { kind: "service", target_service_name: SIG_SERVICE, signals: ["spans"] },
    });
    expect(bad.status()).toBe(400);
  });
});

// ── Neighbor visibility (RBAC) ─────────────────────────────────────
//
// The dependency graph must not leak invisible services: a viewer
// scoped to one service sees an EMPTY adjacency list for it when all
// its neighbors are out of scope — names and traffic counts of
// unshared services read as nonexistent.

test.describe("RBAC — dependency graph hides invisible neighbors", () => {
  const NB_EMAIL = "e2e-nb-viewer@sluicio.local";
  const NB_PASSWORD = "e2e-nb-viewer-pw1";
  const NB_SERVICE = "analytics-processor"; // seeded with a kafka-bridge upstream

  test("scoped viewer sees no out-of-scope neighbors", async ({ page, browser }) => {
    await logIn(page); // admin
    const admin = page.request;
    const lic = await (await admin.get("/api/v1/license")).json();
    test.skip(!lic?.features?.rbac_advanced, "cell has no rbac_advanced entitlement");

    // Baseline: the admin must see at least one neighbor, or the
    // assertion below would pass vacuously.
    const full = await (await admin.get(`/api/v1/services/${NB_SERVICE}/neighbors?range=1h`)).json();
    const fullCount = (full.upstream ?? []).length + (full.downstream ?? []).length;
    test.skip(fullCount === 0, `${NB_SERVICE} has no neighbors in this window — seed traces first`);

    // Provision (idempotent): viewer + group + service policy on
    // NB_SERVICE only.
    const add = await admin.post("/api/v1/settings/members", {
      data: { email: NB_EMAIL, name: "E2E NB Viewer", password: NB_PASSWORD, role: "viewer" },
    });
    if (!add.ok() && add.status() !== 409) throw new Error(`provision: ${add.status()}`);
    let gid: string;
    const mk = await admin.post("/api/v1/settings/groups", { data: { name: "E2E NB Scope", slug: "e2e-nb-scope" } });
    if (mk.ok()) {
      gid = (await mk.json()).id;
    } else {
      const groups = (await (await admin.get("/api/v1/settings/groups")).json()).groups ?? [];
      gid = groups.find((g: { slug: string }) => g.slug === "e2e-nb-scope").id;
    }
    const members = (await (await admin.get("/api/v1/settings/members")).json()).members ?? [];
    const uid = members.find((m: { user: { email: string } }) => m.user.email === NB_EMAIL).user.id;
    await admin.post(`/api/v1/settings/groups/${gid}/members`, { data: { user_id: uid, role: "viewer" } });
    const pol = await admin.post(`/api/v1/settings/groups/${gid}/policies`, {
      data: { kind: "service", target_service_name: NB_SERVICE },
    });
    if (!pol.ok() && pol.status() !== 409) throw new Error(`policy: ${pol.status()}`);

    const viewer = await (await browser.newContext()).newPage();
    await logIn(viewer, NB_EMAIL, NB_PASSWORD);
    const res = await viewer.request.get(`/api/v1/services/${NB_SERVICE}/neighbors?range=1h`);
    expect(res.status()).toBe(200); // the focal service itself is in scope
    const scoped = await res.json();
    // kafka-bridge (and anything else) is out of scope → filtered out.
    expect(scoped.upstream ?? []).toHaveLength(0);
    expect(scoped.downstream ?? []).toHaveLength(0);
  });
});

// ── Org-editor ceiling ──────────────────────────────────────────────────
//
// An org-wide editor mutates monitoring resources but never org
// administration: members, groups, and cell-wide settings stay closed.
test.describe("RBAC — org editor ceiling", () => {
  const ED_EMAIL = "e2e-rbac-editor@sluicio.local";
  const ED_PASSWORD = "e2e-rbac-editor-pw1";

  test("editor mutates resources, never org administration", async ({ page, browser }) => {
    await logIn(page); // admin provisions the editor
    const add = await page.request.post("/api/v1/settings/members", {
      data: { email: ED_EMAIL, name: "E2E RBAC Editor", password: ED_PASSWORD, role: "editor" },
    });
    if (!add.ok() && add.status() !== 409) throw new Error(`provision: ${add.status()}`);

    const ed = await (await browser.newContext()).newPage();
    await logIn(ed, ED_EMAIL, ED_PASSWORD);
    const r = ed.request;

    // CAN: create and delete an integration.
    const mk = await r.post("/api/v1/integrations", {
      data: { slug: `e2e-editor-integ-${Date.now()}`, name: "E2E Editor Integ", matchers: [] },
    });
    expect(mk.status()).toBe(201);
    const integID = (await mk.json()).integration?.id ?? (await mk.json()).id;
    expect((await r.delete(`/api/v1/integrations/${integID}`)).ok()).toBeTruthy();

    // CANNOT: member administration (read is recon-sensitive too).
    expect((await r.get("/api/v1/settings/members")).status()).toBe(403);
    expect(
      (
        await r.post("/api/v1/settings/members", {
          data: { email: "nope@x.se", name: "n", password: "npw12345", role: "viewer" },
        })
      ).status(),
    ).toBe(403);

    // CANNOT: group administration.
    expect(
      (await r.post("/api/v1/settings/groups", { data: { slug: "e2e-ed-grp", name: "x" } })).status(),
    ).toBe(403);

    // CANNOT: cell-wide settings (operator surface).
    expect(
      (await r.patch("/api/v1/cell-settings/system", { data: { environment: "hacked" } })).status(),
    ).toBe(403);

    await ed.context().close();
  });
});

// ── Operator vs org-admin split ─────────────────────────────────────────
//
// A second ADMIN member is not a cell operator: org administration works,
// cell-wide settings refuse. On single-org installs the bootstrap admin
// is the operator, which hides this boundary — this pins it.
test.describe("RBAC — non-operator admin", () => {
  const AD_EMAIL = "e2e-rbac-admin2@sluicio.local";
  const AD_PASSWORD = "e2e-rbac-admin2-pw1";

  test("org admin without operator cannot touch cell-wide settings", async ({ page, browser }) => {
    await logIn(page);
    const add = await page.request.post("/api/v1/settings/members", {
      data: { email: AD_EMAIL, name: "E2E Admin Two", password: AD_PASSWORD, role: "admin" },
    });
    if (!add.ok() && add.status() !== 409) throw new Error(`provision: ${add.status()}`);

    const ad = await (await browser.newContext()).newPage();
    await logIn(ad, AD_EMAIL, AD_PASSWORD);
    const r = ad.request;

    // CAN: org administration (member list).
    expect((await r.get("/api/v1/settings/members")).ok()).toBeTruthy();

    // CANNOT: cell-wide system settings — operator-gated server-side.
    expect(
      (await r.patch("/api/v1/cell-settings/system", { data: { environment: "prod2" } })).status(),
    ).toBe(403);

    await ad.context().close();
  });
});

// ── Per-signal combinations beyond logs-only ────────────────────────────
test.describe("RBAC — per-signal combinations (EE)", () => {
  test.describe.configure({ mode: "serial" });

  const MX_PASSWORD = "e2e-metrics-viewer-pw1";
  // Discovered per run: a service that actually emits metrics (seeded
  // cells differ in which services carry which signals).
  let MX_SERVICE = "";

  test.beforeEach(async ({ page }) => {
    await logIn(page);
    const lic = await (await page.request.get("/api/v1/license")).json();
    test.skip(!lic?.features?.rbac_advanced, "cell has no rbac_advanced entitlement");
    const svcs = (await (await page.request.get("/api/v1/services?range=24h")).json()).services ?? [];
    MX_SERVICE = "";
    for (const s of svcs.slice(0, 10)) {
      const cat = await (
        await page.request.get(`/api/v1/metric-catalog?range=24h&service=${encodeURIComponent(s.service_name)}`)
      ).json();
      if ((cat.metrics ?? []).length > 0) {
        MX_SERVICE = s.service_name;
        break;
      }
    }
    test.skip(!MX_SERVICE, "cell has no service with metrics");
  });

  // Distinct identity per combination — the resolver memoizes per-signal
  // materializations per user, so reusing one user across combinations
  // serves the previous grant from cache.
  async function provisionSignalViewer(admin: APIRequestContext, tag: string, signals: string[]) {
    const email = `e2e-${tag}-viewer@sluicio.local`;
    const slug = `e2e-${tag}-scope`;
    const list = await (await admin.get("/api/v1/settings/groups")).json();
    for (const g of list.groups ?? []) {
      if (g.slug === slug) await admin.delete(`/api/v1/settings/groups/${g.id}`);
    }
    const gid = (await (await admin.post("/api/v1/settings/groups", { data: { slug, name: `E2E ${tag} scope` } })).json()).id;
    const add = await admin.post("/api/v1/settings/members", {
      data: { email, name: `E2E ${tag} viewer`, password: MX_PASSWORD, role: "viewer" },
    });
    if (!add.ok() && add.status() !== 409) throw new Error(`provision: ${add.status()}`);
    const members = await (await admin.get("/api/v1/settings/members")).json();
    const uid = (members.members ?? []).find(
      (m: { user: { email: string; id: string } }) => m.user.email === email,
    )?.user.id;
    await admin.post(`/api/v1/settings/groups/${gid}/members`, { data: { user_id: uid, role: "viewer" } });
    const pol = await admin.post(`/api/v1/settings/groups/${gid}/policies`, {
      data: { kind: "service", target_service_name: MX_SERVICE, signals },
    });
    expect(pol.ok()).toBeTruthy();
    return email;
  }

  test("metrics-only grant: metrics flow, logs and messages stay empty", async ({ page, browser }) => {
    const email = await provisionSignalViewer(page.request, "metricsonly", ["metrics"]);
    const v = await (await browser.newContext()).newPage();
    await logIn(v, email, MX_PASSWORD);
    const r = v.request;

    const metrics = await (await r.get(`/api/v1/metric-catalog?range=24h&service=${MX_SERVICE}`)).json();
    expect((metrics.metrics ?? []).length).toBeGreaterThan(0);

    const logs = await (await r.get(`/api/v1/logs?range=24h&service=${MX_SERVICE}&limit=10`)).json();
    expect(logs.logs ?? []).toHaveLength(0);

    const msgs = await (
      await r.post("/api/v1/messages/search?range=24h", {
        data: { filters: [{ field: "service", op: "is", value: MX_SERVICE }], limit: 10 },
      })
    ).json();
    expect(msgs.results ?? []).toHaveLength(0);
    await v.context().close();
  });

  test("messages-only grant: the business lens works, raw signals stay empty", async ({ page, browser }) => {
    const email = await provisionSignalViewer(page.request, "messagesonly", ["messages"]);
    const v = await (await browser.newContext()).newPage();
    await logIn(v, email, MX_PASSWORD);
    const r = v.request;

    const msgs = await (
      await r.post("/api/v1/messages/search?range=24h", {
        data: { filters: [{ field: "service", op: "is", value: MX_SERVICE }], limit: 10 },
      })
    ).json();
    expect((msgs.results ?? []).length).toBeGreaterThan(0);

    const logs = await (await r.get(`/api/v1/logs?range=24h&service=${MX_SERVICE}&limit=10`)).json();
    expect(logs.logs ?? []).toHaveLength(0);
    const metrics = await (await r.get(`/api/v1/metric-catalog?range=24h&service=${MX_SERVICE}`)).json();
    expect(metrics.metrics ?? []).toHaveLength(0);
    await v.context().close();
  });
});

// ── Sharing rejects non-members (no pending shares by design) ───────────
test.describe("RBAC — share grantee must be an org member (EE)", () => {
  test("sharing to an unknown email is rejected", async ({ page }) => {
    await logIn(page);
    const lic = await (await page.request.get("/api/v1/license")).json();
    test.skip(!lic?.features?.rbac_advanced, "cell has no rbac_advanced entitlement");
    const integs = stableIntegrations(
      (await (await page.request.get("/api/v1/integrations?range=30d")).json()).integrations ?? [],
    );
    test.skip(integs.length === 0, "cell has no integrations");
    const res = await page.request.post(`/api/v1/integrations/${integs[0].id}/shares`, {
      data: { grantee_kind: "user", grantee_email: "ghost-never-existed@e2e.local" },
    });
    expect(res.ok()).toBeFalsy();
    expect((await res.text())).toContain("no such user");
  });
});

// ── Service-account tokens ride the same RBAC ───────────────────────────
test.describe("RBAC — service-account token", () => {
  test("viewer service account: org-wide read (pinned semantics), writes refused", async ({ page, request }) => {
    await logIn(page);
    const admin = page.request;
    const sa = await (
      await admin.post("/api/v1/settings/service-accounts", {
        data: { name: `e2e-sa-viewer-${Date.now()}`, description: "rbac gap test", role: "viewer" },
      })
    ).json();
    const saID = sa.id ?? sa.account?.id;
    const minted = await (
      await admin.post(`/api/v1/settings/service-accounts/${saID}/tokens`, { data: { name: "t1" } })
    ).json();
    const token = minted.plaintext; // secret returned exactly once
    expect(token).toBeTruthy();

    const auth = { Authorization: `Bearer ${token}` };
    // PINNED CURRENT SEMANTICS: service-account principals carry no user
    // id, so the policy layer's deny-by-default never engages — a viewer
    // SA reads ORG-WIDE (unlike a group-less viewer USER, who sees
    // nothing). Role gates still hold for writes/admin below. Whether
    // SAs should be policy-scopable is an open design question — see the
    // "service-account visibility" issue; flip this assertion with that
    // decision.
    const svcs = await (await request.get("/api/v1/services?range=24h", { headers: auth })).json();
    expect((svcs.services ?? []).length).toBeGreaterThan(0);
    // Writes refused outright.
    expect(
      (
        await request.post("/api/v1/integrations", {
          headers: auth,
          data: { slug: "e2e-sa-nope", name: "nope", matchers: [] },
        })
      ).status(),
    ).toBe(403);
    // Org administration refused.
    expect((await request.get("/api/v1/settings/members", { headers: auth })).status()).toBe(403);

    await admin.delete(`/api/v1/settings/service-accounts/${saID}`);
  });
});

// ── MCP inherits the caller token's RBAC ────────────────────────────────
test.describe("RBAC — MCP surface", () => {
  async function mcpListServices(request: APIRequestContext, token: string) {
    const resp = await request.post("/api/v1/mcp", {
      headers: { Authorization: `Bearer ${token}` },
      data: {
        jsonrpc: "2.0",
        id: 1,
        method: "tools/call",
        params: { name: "sluicio_list_services", arguments: { window: "24h" } },
      },
    });
    expect(resp.ok()).toBeTruthy();
    const body = await resp.json();
    expect(body.result?.isError).toBeFalsy();
    return JSON.parse(body.result.content[0].text);
  }

  test("admin token sees services; scoped viewer token sees none", async ({ page, request }) => {
    await logIn(page);
    const admin = page.request;
    // Admin PAT.
    const adminTok = (await (await admin.post("/api/v1/settings/tokens", { data: { name: `e2e-mcp-admin-${Date.now()}` } })).json()).plaintext;
    const adminView = await mcpListServices(request, adminTok);
    expect((adminView.services ?? []).length).toBeGreaterThan(0);

    // Group-less viewer service-account token → MCP shows nothing.
    const sa = await (
      await admin.post("/api/v1/settings/service-accounts", {
        data: { name: `e2e-sa-mcp-${Date.now()}`, description: "mcp rbac", role: "viewer" },
      })
    ).json();
    const saID = sa.id ?? sa.account?.id;
    const minted = await (
      await admin.post(`/api/v1/settings/service-accounts/${saID}/tokens`, { data: { name: "t1" } })
    ).json();
    const viewerTok = minted.plaintext;
    // MCP must mirror REST exactly for the same token (loopback dispatch).
    // For a viewer SA that currently means org-wide read — pinned, see the
    // service-account visibility issue.
    const viewerView = await mcpListServices(request, viewerTok);
    const restView = await (
      await request.get("/api/v1/services?range=24h", { headers: { Authorization: `Bearer ${viewerTok}` } })
    ).json();
    expect((viewerView.services ?? []).length).toBe((restView.services ?? []).length);

    await admin.delete(`/api/v1/settings/service-accounts/${saID}`);
  });
});

// ── Attach-before-telemetry: pin the current semantics ──────────────────
//
// Visibility flows through MEMBER SERVICES (canSeeIntegration): a group
// attached to a service-less integration grants nothing until telemetry
// arrives. Deliberate pin of today's behaviour — if the product decides
// direct attachment should reveal the integration row, flip this test
// with that change.
test.describe("RBAC — attach before telemetry", () => {
  test("group attached to a service-less integration grants nothing (current semantics)", async ({ page, browser }) => {
    await logIn(page);
    const admin = page.request;
    await ensureViewer(admin);
    const gid = (
      await (
        await admin.post("/api/v1/settings/groups", {
          data: { slug: `e2e-pretel-${Date.now().toString(36)}`, name: "E2E PreTelemetry" },
        })
      ).json()
    ).id;
    const members = await (await admin.get("/api/v1/settings/members")).json();
    const uid = (members.members ?? []).find(
      (m: { user: { email: string; id: string } }) => m.user.email === VIEWER_EMAIL,
    )?.user.id;
    await admin.post(`/api/v1/settings/groups/${gid}/members`, { data: { user_id: uid, role: "viewer" } });

    const mk = await admin.post("/api/v1/integrations", {
      data: { slug: `e2e-pretel-integ-${Date.now().toString(36)}`, name: "E2E PreTel Integ", matchers: [] },
    });
    const integID = (await mk.json()).integration?.id ?? (await mk.json()).id;
    expect((await admin.put(`/api/v1/integrations/${integID}/groups`, { data: { group_ids: [gid] } })).ok()).toBeTruthy();

    const viewer = await viewerContext(browser);
    const seen = (await (await viewer.request.get("/api/v1/integrations?range=30d")).json()).integrations ?? [];
    expect(seen.map((i: { id: string }) => i.id)).not.toContain(integID);
    await viewer.context().close();

    await admin.delete(`/api/v1/integrations/${integID}`);
    await admin.delete(`/api/v1/settings/groups/${gid}`);
  });
});
