// SPDX-License-Identifier: Apache-2.0
//
// Admin-set temporary password + forced change on next login. An admin
// sets a temp password for a member; when "require change" is on, the
// member is signed out, and after logging back in with the temp password
// they're gated (403 password_reset_required) on everything except the
// change-password surface until they set a new one.
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { logIn, ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";
const MEMBER_EMAIL = "e2e-forcepw@sluicio.local";
const MEMBER_PW = "e2e-forcepw-initial1";

async function apiLogin(email: string, password: string): Promise<APIRequestContext> {
  const ctx = await pwRequest.newContext({ baseURL: BASE_URL });
  const res = await ctx.post("/api/v1/auth/login", { data: { email, password } });
  if (!res.ok()) throw new Error(`login ${email}: ${res.status()}`);
  return ctx;
}

test.describe.configure({ mode: "serial" });

test.describe("Admin temporary password + forced change", () => {
  let admin: APIRequestContext;
  let memberId = "";

  test.beforeAll(async () => {
    admin = await apiLogin(ADMIN_EMAIL, ADMIN_PASSWORD);
    const add = await admin.post("/api/v1/settings/members", {
      data: { email: MEMBER_EMAIL, name: "E2E ForcePW", password: MEMBER_PW, role: "viewer" },
    });
    if (!add.ok() && add.status() !== 409) throw new Error(`provision: ${add.status()}`);
    const members = await (await admin.get("/api/v1/settings/members")).json();
    memberId = (members.members ?? []).find(
      (m: { user: { email: string } }) => m.user.email === MEMBER_EMAIL,
    ).user.id;
  });

  test("require_change: member is gated until they set a new password", async () => {
    const temp = "temp-Password-123";
    const set = await admin.post(`/api/v1/settings/members/${memberId}/password`, {
      data: { new_password: temp, require_change: true },
    });
    expect(set.status()).toBe(204);

    // Old initial password no longer works (revoked + replaced).
    const stale = await pwRequest.newContext({ baseURL: BASE_URL });
    expect((await stale.post("/api/v1/auth/login", { data: { email: MEMBER_EMAIL, password: MEMBER_PW } })).status()).toBe(401);

    // Log in with the temp password — succeeds, but flag is set.
    const member = await apiLogin(MEMBER_EMAIL, temp);
    const me = await (await member.get("/api/v1/me")).json();
    expect(me.user.must_reset_password).toBe(true);

    // Everything else is blocked with the machine-readable code.
    const blocked = await member.get("/api/v1/integrations");
    expect(blocked.status()).toBe(403);
    expect((await blocked.json()).error).toBe("password_reset_required");

    // The change-password surface stays open; after using it the gate lifts.
    const changed = await member.post("/api/v1/me/password", {
      data: { current_password: temp, new_password: "brand-New-pass-9" },
    });
    expect(changed.status()).toBe(204);
    expect((await member.get("/api/v1/integrations")).status()).toBe(200);
  });

  test("require_change=false: no gate, temp password works immediately", async () => {
    const temp = "temp-nogate-456";
    const set = await admin.post(`/api/v1/settings/members/${memberId}/password`, {
      data: { new_password: temp, require_change: false },
    });
    expect(set.status()).toBe(204);
    const member = await apiLogin(MEMBER_EMAIL, temp);
    expect((await (await member.get("/api/v1/me")).json()).user.must_reset_password).toBe(false);
    expect((await member.get("/api/v1/integrations")).status()).toBe(200);
  });

  test("the browser lands on the forced-change screen, not the app", async ({ browser }) => {
    const temp = "temp-browser-789";
    await admin.post(`/api/v1/settings/members/${memberId}/password`, {
      data: { new_password: temp, require_change: true },
    });
    const page = await (await browser.newContext()).newPage();
    await page.goto("/");
    await page.getByLabel("Email").fill(MEMBER_EMAIL);
    await page.getByLabel("Password").fill(temp);
    await page.getByRole("button", { name: /^Sign in$/ }).click();
    await expect(page.getByText("Choose a new password")).toBeVisible({ timeout: 15_000 });
    // The normal app chrome (nav) must NOT be present.
    await expect(page.getByText("Dashboard")).toHaveCount(0);
  });

  test("admins can't reset their own password here, and target must be in-org", async () => {
    // Self is refused (use Account instead).
    const meRes = await (await admin.get("/api/v1/me")).json();
    const selfId = meRes.user.id;
    const self = await admin.post(`/api/v1/settings/members/${selfId}/password`, {
      data: { new_password: "whatever-123", require_change: true },
    });
    expect(self.status()).toBe(400);

    // A random (non-member) id → 404.
    const foreign = await admin.post(
      "/api/v1/settings/members/00000000-0000-0000-0000-0000000000ff/password",
      { data: { new_password: "whatever-123" } },
    );
    expect(foreign.status()).toBe(404);
  });
});
