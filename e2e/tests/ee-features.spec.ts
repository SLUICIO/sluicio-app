// SPDX-License-Identifier: Apache-2.0
//
// Enterprise entitlements, verified live against a licensed cell:
//   - retention_long: values beyond the free 14-day cap are accepted
//   - license status: all five features reported entitled
//   - mfa_policy: enabling requires the operator to be enrolled first;
//     once on, an unenrolled member is locked to the enrollment surface
//     (server-side 403, not just the banner) and the UI funnels them to
//     Account → Two-factor. Enrollment uses real TOTP codes.
//
// The CE side of these gates (402s without a license) is probed in the
// handler layer and was verified manually against an unlicensed cell —
// this suite assumes the dev cell's enterprise license.
import { createHmac } from "crypto";
import {
  test,
  expect,
  request as pwRequest,
  type APIRequestContext,
  type Browser,
} from "@playwright/test";
import { logIn, ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";
const OP_EMAIL = "e2e-mfa-op@sluicio.local";
const OP_PASSWORD = "e2e-mfa-op-pw1";
const MEMBER_EMAIL = "e2e-mfa-member@sluicio.local";
const MEMBER_PASSWORD = "e2e-mfa-member-pw1";

// ── minimal TOTP (RFC 6238, SHA-1, 6 digits, 30s) ────────────────────
function base32Decode(s: string): Buffer {
  const A = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = 0;
  let value = 0;
  const out: number[] = [];
  for (const ch of s.replace(/=+$/, "").toUpperCase()) {
    const idx = A.indexOf(ch);
    if (idx < 0) continue;
    value = (value << 5) | idx;
    bits += 5;
    if (bits >= 8) {
      out.push((value >>> (bits - 8)) & 0xff);
      bits -= 8;
    }
  }
  return Buffer.from(out);
}

function totp(secret: string, at = Date.now()): string {
  const key = base32Decode(secret);
  const buf = Buffer.alloc(8);
  buf.writeBigUInt64BE(BigInt(Math.floor(at / 1000 / 30)));
  const h = createHmac("sha1", key).update(buf).digest();
  const off = h[h.length - 1] & 0xf;
  const code =
    (((h[off] & 0x7f) << 24) | (h[off + 1] << 16) | (h[off + 2] << 8) | h[off + 3]) % 1e6;
  return code.toString().padStart(6, "0");
}

// apiLogin returns a standalone request context holding a session cookie.
async function apiLogin(email: string, password: string): Promise<APIRequestContext> {
  const ctx = await pwRequest.newContext({ baseURL: BASE_URL });
  const res = await ctx.post("/api/v1/auth/login", { data: { email, password } });
  if (!res.ok()) throw new Error(`login ${email}: ${res.status()}`);
  return ctx;
}

// ensureUser (idempotent) provisions a member and returns its user id.
async function ensureUser(
  admin: APIRequestContext,
  email: string,
  password: string,
  role: string,
): Promise<string> {
  const res = await admin.post("/api/v1/settings/members", {
    data: { email, name: email.split("@")[0], password, role },
  });
  if (!res.ok() && res.status() !== 409) {
    throw new Error(`provision ${email}: ${res.status()}`);
  }
  const members = await (await admin.get("/api/v1/settings/members")).json();
  const row = (members.members ?? []).find(
    (m: { user: { email: string } }) => m.user.email === email,
  );
  if (!row) throw new Error(`${email} not in member list`);
  return row.user.id;
}

test.describe.configure({ mode: "serial" });

test.describe("Enterprise entitlements (licensed cell)", () => {
  test("license reports all five features entitled", async ({ page }) => {
    await logIn(page);
    const st = await (await page.request.get("/api/v1/license")).json();
    expect(st.licensed).toBe(true);
    for (const f of ["sso", "rbac_advanced", "audit_log", "retention_long", "mfa_policy"]) {
      expect(st.features[f], f).toBe(true);
    }
  });

  test("retention_long accepts values beyond the free cap", async ({ page }) => {
    await logIn(page);
    const before = await (await page.request.get("/api/v1/cell-settings/retention")).json();
    expect(before.long_retention).toBe(true);
    const res = await page.request.patch("/api/v1/cell-settings/retention", {
      data: { traces_days: 30 },
    });
    expect(res.status()).toBe(200);
    // Revert to whatever it was.
    await page.request.patch("/api/v1/cell-settings/retention", {
      data: { traces_days: before.traces.days },
    });
  });
});

test.describe("Enterprise MFA policy", () => {
  let admin: APIRequestContext; // seed admin (never enrolled — stays safe)
  let op: APIRequestContext; // dedicated operator that enrolls
  let opSecret = "";

  test.beforeAll(async () => {
    admin = await apiLogin(ADMIN_EMAIL, ADMIN_PASSWORD);
    const opID = await ensureUser(admin, OP_EMAIL, OP_PASSWORD, "admin");
    await ensureUser(admin, MEMBER_EMAIL, MEMBER_PASSWORD, "viewer");
    const flag = await admin.put(`/api/v1/operator/users/${opID}/operator`, {
      data: { is_operator: true },
    });
    if (!flag.ok()) throw new Error(`operator flag: ${flag.status()}`);
    op = await apiLogin(OP_EMAIL, OP_PASSWORD);
  });

  test.afterAll(async () => {
    // Leave the cell how we found it even after a mid-test failure: policy
    // off, operator's MFA unenrolled. Best-effort.
    try {
      await op.patch("/api/v1/cell-settings/security", { data: { mfa_required: false } });
    } catch {
      /* already off */
    }
    if (opSecret) {
      try {
        await op.post("/api/v1/account/mfa/disable", { data: { code: totp(opSecret) } });
      } catch {
        /* not enabled */
      }
    }
  });

  test("enabling the policy while unenrolled is refused", async () => {
    const res = await op.patch("/api/v1/cell-settings/security", {
      data: { mfa_required: true },
    });
    expect(res.status()).toBe(400);
    expect((await res.text())).toContain("your own account first");
  });

  test("unenrolled member is locked to the enrollment surface", async ({ browser }) => {
    // Operator enrolls with a real TOTP code, then flips the policy on.
    const setup = await (await op.post("/api/v1/account/mfa/setup")).json();
    opSecret = setup.secret;
    const enable = await op.post("/api/v1/account/mfa/enable", {
      data: { code: totp(opSecret) },
    });
    expect(enable.status()).toBe(200);
    const on = await op.patch("/api/v1/cell-settings/security", {
      data: { mfa_required: true },
    });
    expect(on.status()).toBe(200);

    const member = await apiLogin(MEMBER_EMAIL, MEMBER_PASSWORD);
    // Blocked outside the enrollment surface, with the machine-readable code.
    const blocked = await member.get("/api/v1/integrations");
    expect(blocked.status()).toBe(403);
    expect((await blocked.json()).error).toBe("mfa_enrollment_required");
    // The enrollment surface stays reachable.
    expect((await member.get("/api/v1/me")).status()).toBe(200);
    expect((await member.get("/api/v1/account/mfa")).status()).toBe(200);
    const me = await (await member.get("/api/v1/me")).json();
    expect(me.mfa_enrollment_required).toBe(true);

    // The UI funnels a browser session to Account → Two-factor.
    const page = await (await browser.newContext()).newPage();
    await page.goto("/");
    await page.getByLabel("Email").fill(MEMBER_EMAIL);
    await page.getByLabel("Password").fill(MEMBER_PASSWORD);
    await page.getByRole("button", { name: /^Sign in$/ }).click();
    await expect(page).toHaveURL(/\/account\?tab=mfa$/, { timeout: 15_000 });
    await expect(page.getByText("Two-factor authentication is required")).toBeVisible();

    // Off again: the member is immediately unblocked.
    const off = await op.patch("/api/v1/cell-settings/security", {
      data: { mfa_required: false },
    });
    expect(off.status()).toBe(200);
    expect((await member.get("/api/v1/integrations")).status()).toBe(200);

    // Operator unenrolls (cleanup also covered by afterAll).
    const disable = await op.post("/api/v1/account/mfa/disable", {
      data: { code: totp(opSecret) },
    });
    expect(disable.status()).toBe(200);
    opSecret = "";
  });
});
