// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Lost password, from the login page to signing in with the new one,
// through a real SMTP conversation. The endpoint answers the same whatever
// happens, so only the mail arriving proves anything: this flow stopped
// being checkable the moment nobody looked in a mailbox.
//
//   E2E_SMTP_HOST     SMTP catcher host, as the CELL sees it
//   E2E_SMTP_PORT     its port (default 1025)
//   E2E_MAILPIT_API   its API, as THIS process sees it

import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { logIn } from "./fixtures";

const SMTP_HOST = process.env.E2E_SMTP_HOST || "";
const SMTP_PORT = process.env.E2E_SMTP_PORT || "1025";
const MAILPIT_API = process.env.E2E_MAILPIT_API || "";

type Inbox = { messages?: { ID: string; Subject: string }[] };

test.describe("Lost password", () => {
  test.describe.configure({ mode: "serial" });
  test.skip(!SMTP_HOST || !MAILPIT_API, "no SMTP catcher configured (E2E_SMTP_HOST + E2E_MAILPIT_API)");

  const stamp = Date.now().toString(36);
  const email = `e2e-reset-${stamp}@test.local`;
  const firstPassword = `first-${stamp}-password`;
  const newPassword = `second-${stamp}-password`;
  let previous: { host: string; port: string; username: string; from: string; from_name: string };

  const inboxOf = async (request: APIRequestContext, to: string): Promise<Inbox> =>
    (await request.get(`${MAILPIT_API}/api/v1/search?query=${encodeURIComponent(`to:${to}`)}`)).json();

  const askForReset = async (page: Page, address: string) => {
    await page.goto("/");
    await page.getByRole("button", { name: "Forgot password?" }).click();
    await page.getByLabel("Email").fill(address);
    await page.getByRole("button", { name: "Send reset link" }).click();
    await expect(page.getByText(/we've sent a password-reset link/)).toBeVisible();
  };

  test("set up: a member, and this cell's mail going to the catcher", async ({ page }) => {
    await logIn(page);
    const admin = page.request;
    previous = await (await admin.get("/api/v1/cell-settings/smtp")).json();
    const smtp = await admin.patch("/api/v1/cell-settings/smtp", {
      data: { host: SMTP_HOST, port: SMTP_PORT, from: "noreply@e2e.local", from_name: "Sluicio" },
    });
    expect(smtp.ok()).toBeTruthy();
    const member = await admin.post("/api/v1/settings/members", {
      data: { email, name: "E2E Reset", password: firstPassword, role: "viewer" },
    });
    expect(member.ok()).toBeTruthy();
  });

  test("the mail arrives, its link sets a new password, and the old one stops working", async ({ browser, request }) => {
    const anon = await browser.newContext();
    const page = await anon.newPage();
    await askForReset(page, email);

    let found: Inbox["messages"];
    await expect
      .poll(async () => (found = (await inboxOf(request, email)).messages ?? []).length, {
        message: "the reset mail did not arrive",
        timeout: 15_000,
      })
      .toBe(1);
    expect(found![0].Subject).toContain("Reset your Sluicio password");

    // 7-bit on the wire: the plain-ASCII test mail can't reveal this, and
    // a body with a raw non-ASCII byte is up to each relay on the way.
    const raw = await (await request.get(`${MAILPIT_API}/api/v1/message/${found![0].ID}/raw`)).text();
    expect(raw).toContain("Content-Transfer-Encoding: quoted-printable");
    // eslint-disable-next-line no-control-regex
    expect(raw).toMatch(/^[\x00-\x7f]*$/);

    const body = (await (await request.get(`${MAILPIT_API}/api/v1/message/${found![0].ID}`)).json()).Text as string;
    const token = /reset-password\?token=([\w-]+)/.exec(body)?.[1];
    expect(token, "no reset link in the mail").toBeTruthy();

    await page.goto(`/reset-password?token=${token}`);
    const fields = page.locator('input[type="password"]');
    await fields.nth(0).fill(newPassword);
    await fields.nth(1).fill(newPassword);
    await page.getByRole("button", { name: /password/i }).click();
    await expect(page.getByText(/Your password has been updated/)).toBeVisible();
    await anon.close();

    // Single use, and the old password is gone.
    const again = await request.post("/api/v1/auth/reset-password", { data: { token, new_password: newPassword } });
    expect(again.status()).toBe(400);
    const old = await request.post("/api/v1/auth/login", { data: { email, password: firstPassword } });
    expect(old.status()).toBe(401);
    const signedIn = await request.post("/api/v1/auth/login", { data: { email, password: newPassword } });
    expect(signedIn.ok()).toBeTruthy();
  });

  test("an unknown address gets the same answer and no mail", async ({ browser, request }) => {
    const nobody = `e2e-nobody-${stamp}@test.local`;
    const anon = await browser.newContext();
    await askForReset(await anon.newPage(), nobody);
    await anon.close();
    await new Promise((r) => setTimeout(r, 2000));
    expect((await inboxOf(request, nobody)).messages ?? []).toHaveLength(0);
  });

  test("a cell that can't send mail says so instead of promising a link", async ({ page, browser }) => {
    await logIn(page);
    const off = await page.request.patch("/api/v1/cell-settings/smtp", { data: { host: "", port: "", from: "" } });
    expect(off.ok()).toBeTruthy();
    const state = await (await page.request.get("/api/v1/auth/install-state")).json();
    test.skip(state.can_send_mail !== false, "this cell's mail is set by its environment");

    const anon = await browser.newContext();
    const visitor = await anon.newPage();
    await visitor.goto("/");
    await visitor.getByRole("button", { name: "Forgot password?" }).click();
    await expect(visitor.getByTestId("forgot-no-mail")).toContainText("Ask your administrator");
    await expect(visitor.getByRole("button", { name: "Send reset link" })).toHaveCount(0);
    await anon.close();
  });

  test.afterAll(async ({ browser }) => {
    if (!previous) return;
    const ctx = await browser.newContext();
    const page = await ctx.newPage();
    await logIn(page);
    await page.request.patch("/api/v1/cell-settings/smtp", {
      data: {
        host: previous.host ?? "",
        port: previous.port ?? "",
        username: previous.username ?? "",
        from: previous.from ?? "",
        from_name: previous.from_name ?? "",
      },
    });
    await ctx.close();
  });
});
