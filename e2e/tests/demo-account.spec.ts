// SPDX-License-Identifier: Apache-2.0
//
// Demo-account guard — a user flagged is_demo keeps full RBAC-driven
// product access but loses the self-service account surface (profile,
// password, MFA, tokens), so one visitor to a shared demo login can't
// sabotage it for the next. The flag is operator-set.
import { test, expect, type APIRequestContext, type Browser } from "@playwright/test";
import { logIn } from "./fixtures";

const DEMO_EMAIL = "e2e-demo@sluicio.local";
const DEMO_PASSWORD = "e2e-demo-pw1";

// ensureDemoUser (idempotent): provision the member, look up its id,
// and set the demo flag via the operator endpoint.
async function ensureDemoUser(admin: APIRequestContext): Promise<string> {
  const res = await admin.post("/api/v1/settings/members", {
    data: { email: DEMO_EMAIL, name: "E2E Demo", password: DEMO_PASSWORD, role: "viewer" },
  });
  if (!res.ok() && res.status() !== 409) {
    throw new Error(`could not provision demo user: ${res.status()}`);
  }
  const members = await (await admin.get("/api/v1/settings/members")).json();
  const row = (members.members ?? []).find(
    (m: { user: { email: string } }) => m.user.email === DEMO_EMAIL,
  );
  if (!row) throw new Error("demo user not in member list");
  const flag = await admin.put(`/api/v1/operator/users/${row.user.id}/demo`, {
    data: { is_demo: true },
  });
  if (!flag.ok()) throw new Error(`could not set demo flag: ${flag.status()}`);
  return row.user.id;
}

async function demoContext(browser: Browser) {
  const page = await (await browser.newContext()).newPage();
  await logIn(page, DEMO_EMAIL, DEMO_PASSWORD);
  return page;
}

// Serial: the flag-clearing test would race the others' demo logins.
test.describe.configure({ mode: "serial" });

test.describe("Demo-account guard", () => {
  test.beforeEach(async ({ page }) => {
    await logIn(page); // admin (seed admin is the cell operator)
    await ensureDemoUser(page.request);
  });

  test("self-service endpoints all return 403", async ({ browser }) => {
    const page = await demoContext(browser);
    const probes: Array<[string, Promise<{ status(): number }>]> = [
      ["PATCH /me", page.request.patch("/api/v1/me", { data: { name: "hijacked" } })],
      [
        "POST /me/password",
        page.request.post("/api/v1/me/password", {
          data: { current_password: DEMO_PASSWORD, new_password: "hijacked-pw-123" },
        }),
      ],
      ["POST /account/mfa/setup", page.request.post("/api/v1/account/mfa/setup")],
      [
        "POST /settings/tokens",
        page.request.post("/api/v1/settings/tokens", { data: { name: "sneaky" } }),
      ],
    ];
    for (const [label, p] of probes) {
      expect((await p).status(), label).toBe(403);
    }
    // Read surfaces stay open — the account still works as a product user.
    expect((await page.request.get("/api/v1/me")).ok()).toBeTruthy();
    expect((await page.request.get("/api/v1/integrations")).ok()).toBeTruthy();
  });

  test("Account page shows the demo notice instead of forms", async ({ browser }) => {
    const page = await demoContext(browser);
    await page.goto("/account");
    await expect(page.getByText("shared demo account")).toBeVisible();
    await expect(page.getByLabel("Current password")).toHaveCount(0);
    // Theme is a local preference, not shared state — still available.
    await page.goto("/account?tab=theme");
    await expect(page.getByText(/theme/i).first()).toBeVisible();
  });

  test("clearing the flag restores self-service", async ({ page, browser }) => {
    const members = await (await page.request.get("/api/v1/settings/members")).json();
    const row = (members.members ?? []).find(
      (m: { user: { email: string } }) => m.user.email === DEMO_EMAIL,
    );
    const id = row.user.id;
    await page.request.put(`/api/v1/operator/users/${id}/demo`, { data: { is_demo: false } });

    const demo = await demoContext(browser);
    const res = await demo.request.patch("/api/v1/me", { data: { name: "E2E Demo" } });
    expect(res.ok()).toBeTruthy();

    // Restore the flag for other tests / future runs.
    await page.request.put(`/api/v1/operator/users/${id}/demo`, { data: { is_demo: true } });
  });
});

// A demo account may hold an ADMIN role to showcase the admin surfaces —
// but org-destructive administration stays blocked: org lifecycle, member
// management, SSO config, ingest keys. Product-level admin work (tags,
// groups, integrations) remains available so the demo is explorable.
test.describe("Demo-account guard — admin role", () => {
  const DA_EMAIL = "e2e-demo-admin@sluicio.local";
  const DA_PASSWORD = "e2e-demo-admin-pw1";

  test("demo admin cannot touch org lifecycle, members, SSO, or ingest keys", async ({ page, browser }) => {
    await logIn(page); // seed admin (operator)
    const admin = page.request;
    const add = await admin.post("/api/v1/settings/members", {
      data: { email: DA_EMAIL, name: "E2E Demo Admin", password: DA_PASSWORD, role: "admin" },
    });
    if (!add.ok() && add.status() !== 409) throw new Error(`provision: ${add.status()}`);
    const members = await (await admin.get("/api/v1/settings/members")).json();
    const row = (members.members ?? []).find(
      (m: { user: { email: string } }) => m.user.email === DA_EMAIL,
    );
    const flag = await admin.put(`/api/v1/operator/users/${row.user.id}/demo`, { data: { is_demo: true } });
    if (!flag.ok()) throw new Error(`demo flag: ${flag.status()}`);

    const demo = await (await browser.newContext()).newPage();
    await logIn(demo, DA_EMAIL, DA_PASSWORD);
    const me = await (await demo.request.get("/api/v1/me")).json();
    const orgId = me.principal.org_id;

    // Org deletion is operator-only now: the old admin route is gone
    // (405 — other methods still live on the pattern) and the operator
    // route rejects non-operators.
    expect((await demo.request.delete(`/api/v1/orgs/${orgId}`)).status()).toBe(405);
    expect((await demo.request.delete(`/api/v1/operator/orgs/${orgId}`)).status()).toBe(403);

    const probes: Array<[string, Promise<{ status(): number }>]> = [
      ["PATCH org", demo.request.patch(`/api/v1/orgs/${orgId}`, { data: { name: "hijacked" } })],
      [
        "POST members (invite)",
        demo.request.post("/api/v1/settings/members", {
          data: { email: "sneak@x.io", name: "x", password: "12345678", role: "admin" },
        }),
      ],
      ["DELETE member", demo.request.delete(`/api/v1/settings/members/${row.user.id}`)],
      ["POST ingest-keys", demo.request.post("/api/v1/ingest-keys", { data: { name: "sneak" } })],
    ];
    for (const [label, pr] of probes) {
      const res = await pr;
      expect(res.status(), label).toBe(403);
      expect((await res.text()), label).toContain("shared demo account");
    }

    // SSO config sits behind the enterprise gate too: an unlicensed cell
    // (like CI) answers 402 before the demo guard can 403. Either way the
    // surface is closed to the demo account.
    const sso = await demo.request.post("/api/v1/settings/auth-providers", { data: { name: "x" } });
    expect([402, 403], "POST auth-providers").toContain(sso.status());
    if (sso.status() === 403) {
      expect(await sso.text()).toContain("shared demo account");
    }

    // Product-level admin work stays open — the demo must remain a real demo.
    const slug = `e2e-demo-tag-${Date.now()}`;
    const tag = await demo.request.post("/api/v1/tags", { data: { slug, name: "Demo probe", color: "#5566ee" } });
    expect(tag.status()).toBe(201);
    await demo.request.delete(`/api/v1/tags/${(await tag.json()).id}`);
  });
});
