// SPDX-License-Identifier: Apache-2.0
//
// Collector management (Beta), end to end: the switch, team tokens, the
// registry filled by simulated OpAMP agents, team scoping, the
// collector's page with its tabs and views, masking, removal and the MCP
// read tools.
//
// Collectors come from services/cell-api/cmd/opamp-fake-agent (opamp-go),
// built once per run, connecting straight to cell-api:
//   E2E_OPAMP_URL  the OpAMP endpoint (default <E2E_API_URL>/api/v1/opamp)
//
// The suite follows the cell: with collector management off only the
// "switched off" test runs; set E2E_EXPECT_COLLECTORS=1 to make a cell
// that has it off fail instead of skip.
import { test, expect, type APIRequestContext, type Browser } from "@playwright/test";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { logIn } from "./fixtures";

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const API_URL = process.env.E2E_API_URL || "http://localhost:8081";
const OPAMP_URL = process.env.E2E_OPAMP_URL || `${API_URL}/api/v1/opamp`;
const EXPECT_ON = ["1", "true", "yes"].includes((process.env.E2E_EXPECT_COLLECTORS ?? "").toLowerCase());
// The default configuration the fake agent reports carries this header.
const AGENT_SECRET = "fake-agent-secret";

let agentBinary = "";
function fakeAgent(token: string, host: string, n = 1): void {
  if (!agentBinary) {
    agentBinary = path.join(mkdtempSync(path.join(tmpdir(), "opamp-agent-")), "opamp-fake-agent");
    execFileSync("go", ["build", "-o", agentBinary, "./services/cell-api/cmd/opamp-fake-agent"], { cwd: REPO, stdio: "inherit" });
  }
  // -once: exit after every agent delivered its first message. The
  // collectors then read offline, which is what removal needs.
  const r = spawnSync(agentBinary, ["-url", OPAMP_URL, "-token", token, "-n", String(n), "-host", host, "-http-share", "0.5", "-once"], {
    encoding: "utf8",
    timeout: 30_000,
  });
  if (r.status !== 0) throw new Error(`fake agent failed: ${r.stderr}`);
}

async function collectorsOn(request: APIRequestContext): Promise<boolean> {
  return (await (await request.get("/api/v1/me")).json()).collectors_enabled === true;
}

async function ensureUser(admin: APIRequestContext, email: string, password: string): Promise<string> {
  const res = await admin.post("/api/v1/settings/members", { data: { email, name: email, password, role: "viewer" } });
  if (!res.ok() && res.status() !== 409) throw new Error(`provision ${email}: ${res.status()}`);
  const members = await (await admin.get("/api/v1/settings/members")).json();
  const uid = (members.members ?? []).find((m: { user: { email: string; id: string } }) => m.user.email === email)?.user.id;
  if (!uid) throw new Error(`no user id for ${email}`);
  return uid;
}

async function makeTeam(admin: APIRequestContext, slug: string): Promise<string> {
  const res = await admin.post("/api/v1/settings/groups", { data: { slug, name: slug } });
  expect(res.ok(), `create team ${slug}: ${res.status()}`).toBeTruthy();
  return (await res.json()).id;
}

async function addToTeam(admin: APIRequestContext, team: string, userId: string, role: string) {
  const res = await admin.post(`/api/v1/settings/groups/${team}/members`, { data: { user_id: userId, role } });
  expect(res.ok(), `add member: ${res.status()}`).toBeTruthy();
}

async function mintToken(ctx: APIRequestContext, team: string): Promise<string> {
  const res = await ctx.post(`/api/v1/settings/groups/${team}/collector-tokens`, { data: { name: "e2e" } });
  expect(res.status()).toBe(201);
  return (await res.json()).token;
}

async function userPage(browser: Browser, email: string, password: string) {
  const page = await (await browser.newContext()).newPage();
  await logIn(page, email, password);
  return page;
}

test.describe("Collector management (Beta)", () => {
  test.describe.configure({ mode: "serial" });
  const stamp = Date.now().toString(36);
  const ADMIN_PW = "e2e-col-admin-pw1";
  const VIEWER_PW = "e2e-col-viewer-pw1";
  const teamAdminEmail = `e2e-col-admin-${stamp}@sluicio.local`;
  const otherViewerEmail = `e2e-col-other-${stamp}@sluicio.local`;
  const teams: string[] = [];

  test.afterAll(async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    await logIn(page);
    const admin = page.request;
    if (await collectorsOn(admin)) {
      for (const c of (await (await admin.get("/api/v1/collectors")).json()).collectors ?? []) {
        if (teams.includes(c.group_id)) await admin.delete(`/api/v1/collectors/${c.id}`);
      }
    }
    for (const id of teams) await admin.delete(`/api/v1/settings/groups/${id}`);
    await page.context().close();
  });

  test("switched off: no entry, no page, no routes", async ({ page }) => {
    await logIn(page);
    test.skip(await collectorsOn(page.request), "collector management is on for this cell");
    await expect(page.getByRole("link", { name: /Collectors/ })).toHaveCount(0);
    expect((await page.request.get("/api/v1/collectors")).status()).toBe(404);
    expect((await page.request.post(`${API_URL}/api/v1/opamp`, { data: "" })).status()).toBe(404);
    await page.goto("/collectors");
    await expect(page.getByText(/not turned on for this cell/)).toBeVisible();
  });

  test("team admin manages tokens; a team viewer cannot", async ({ page, browser }) => {
    await logIn(page);
    const on = await collectorsOn(page.request);
    if (!on && EXPECT_ON) throw new Error("E2E_EXPECT_COLLECTORS is set but collector management is off");
    test.skip(!on, "collector management is off for this cell");
    const admin = page.request;
    const team = await makeTeam(admin, `e2e-col-a-${stamp}`);
    teams.push(team);
    await addToTeam(admin, team, await ensureUser(admin, teamAdminEmail, ADMIN_PW), "admin");
    await addToTeam(admin, team, await ensureUser(admin, otherViewerEmail, VIEWER_PW), "viewer");

    const teamAdmin = await userPage(browser, teamAdminEmail, ADMIN_PW);
    await teamAdmin.goto("/collectors");
    await expect(teamAdmin.getByRole("heading", { name: /Collectors/ })).toBeVisible();
    await expect(teamAdmin.getByText("Beta").first()).toBeVisible();
    const section = teamAdmin.locator("section", { has: teamAdmin.getByRole("heading", { name: `e2e-col-a-${stamp}` }) });
    await section.getByRole("button", { name: "Tokens" }).click();
    await teamAdmin.getByPlaceholder(/Token name/).fill("prod hosts");
    await teamAdmin.getByRole("button", { name: "Create token" }).click();
    const shown = teamAdmin.locator(".mono", { hasText: /^sct_[A-Za-z0-9_-]{30,}$/ });
    await expect(shown).toBeVisible();
    const first = (await shown.textContent())!.trim();
    teamAdmin.on("dialog", (d) => d.accept());
    await teamAdmin.getByRole("button", { name: "Regenerate" }).click();
    await expect(shown).not.toHaveText(first);
    await teamAdmin.getByRole("button", { name: "Revoke" }).click();
    await expect(teamAdmin.getByText("No tokens yet.")).toBeVisible();

    const viewer = await userPage(browser, otherViewerEmail, VIEWER_PW);
    await viewer.goto("/collectors");
    const vSection = viewer.locator("section", { has: viewer.getByRole("heading", { name: `e2e-col-a-${stamp}` }) });
    await expect(vSection).toBeVisible();
    await expect(vSection.getByRole("button", { name: "Tokens" })).toHaveCount(0);
    expect((await viewer.request.post(`/api/v1/settings/groups/${team}/collector-tokens`, { data: { name: "x" } })).status()).toBe(404);
  });

  test("agents land in their token's team, and only that team sees them", async ({ page, browser }) => {
    await logIn(page);
    test.skip(!(await collectorsOn(page.request)), "collector management is off for this cell");
    const admin = page.request;
    const teamA = teams[0];
    const teamB = await makeTeam(admin, `e2e-col-b-${stamp}`);
    teams.push(teamB);
    fakeAgent(await mintToken(admin, teamA), `e2e-a-${stamp}`, 2);
    fakeAgent(await mintToken(admin, teamB), `e2e-b-${stamp}`, 1);

    const list = (await (await admin.get("/api/v1/collectors")).json()).collectors;
    const inA = list.filter((c: { group_id: string }) => c.group_id === teamA);
    const inB = list.filter((c: { group_id: string }) => c.group_id === teamB);
    expect(inA.map((c: { description: Record<string, string> }) => c.description["host.name"]).sort()).toEqual([`e2e-a-${stamp}-0`, `e2e-a-${stamp}-1`]);
    expect(inB).toHaveLength(1);
    expect(inA.every((c: { kind: string }) => c.kind === "extension")).toBeTruthy();

    // The team-A viewer reads team A only; a direct read of B's is 404.
    const viewer = await userPage(browser, otherViewerEmail, VIEWER_PW);
    const seen = (await (await viewer.request.get("/api/v1/collectors")).json()).collectors.map((c: { id: string }) => c.id);
    expect(seen).toEqual(expect.arrayContaining(inA.map((c: { id: string }) => c.id)));
    expect(seen).not.toContain(inB[0].id);
    expect((await viewer.request.get(`/api/v1/collectors/${inB[0].id}`)).status()).toBe(404);
    await viewer.goto("/collectors");
    await expect(viewer.getByText(`e2e-a-${stamp}-0`)).toBeVisible();
    await expect(viewer.getByText(`e2e-b-${stamp}-0`)).toHaveCount(0);
  });

  test("a collector's page: tabs, views, and no secret anywhere", async ({ page }) => {
    await logIn(page);
    test.skip(!(await collectorsOn(page.request)), "collector management is off for this cell");
    const list = (await (await page.request.get("/api/v1/collectors")).json()).collectors;
    const c = list.find((x: { description: Record<string, string> }) => x.description["host.name"] === `e2e-a-${stamp}-0`);
    expect(c).toBeTruthy();

    const detail = await page.request.get(`/api/v1/collectors/${c.id}`);
    const cfg = await page.request.get(`/api/v1/collectors/${c.id}/configs/latest`);
    expect(await detail.text()).not.toContain(AGENT_SECRET);
    if (cfg.ok()) expect(await cfg.text()).not.toContain(AGENT_SECRET);

    await page.goto(`/collectors/${c.id}`);
    for (const tab of ["Agent", "Health", "Configuration"]) {
      await expect(page.getByRole("button", { name: new RegExp(`^${tab}`) })).toBeVisible();
    }
    await expect(page.getByText(`e2e-a-${stamp}-0`).first()).toBeVisible();
    await page.getByRole("button", { name: /^Configuration/ }).click();
    await expect(page).toHaveURL(/tab=configuration/);
    if (!(await cfg.ok())) return; // no key on this cell: nothing more to see
    const me = await (await page.request.get("/api/v1/me")).json();
    if (me.otelflow_url) {
      const frame = page.locator("iframe");
      await expect(frame).toHaveAttribute("src", /^\/otelflow\/#embed=/);
      await page.getByRole("radio", { name: "YAML" }).click();
      await expect(page).toHaveURL(/view=yaml/);
      await expect(frame).toHaveAttribute("src", /view=config/);
    } else {
      await expect(page.getByText("receivers:")).toBeVisible();
    }
    expect(await page.content()).not.toContain(AGENT_SECRET);
  });

  test("a team that owns collectors is kept until they are removed", async ({ page }) => {
    await logIn(page);
    test.skip(!(await collectorsOn(page.request)), "collector management is off for this cell");
    const admin = page.request;
    const teamB = teams[1];
    const del = await admin.delete(`/api/v1/settings/groups/${teamB}`);
    expect(del.status()).toBe(409);
    expect(await del.text()).toContain("collectors");
    const mine = (await (await admin.get("/api/v1/collectors")).json()).collectors.filter((c: { group_id: string }) => c.group_id === teamB);
    // -once agents have gone; WebSocket ones are offline at once, HTTP
    // ones once their 90s window has passed.
    for (const c of mine) {
      await expect.poll(async () => (await admin.delete(`/api/v1/collectors/${c.id}`)).status(), { timeout: 120_000, intervals: [5_000] }).toBe(204);
    }
    expect((await admin.delete(`/api/v1/settings/groups/${teamB}`)).status()).toBe(204);
    teams.splice(teams.indexOf(teamB), 1);
  });

  test("MCP read tools see the caller's teams, masked", async ({ page }) => {
    await logIn(page);
    test.skip(!(await collectorsOn(page.request)), "collector management is off for this cell");
    const tok = await page.request.post("/api/v1/settings/tokens", { data: { name: `e2e-col-mcp-${stamp}` } });
    test.skip(!tok.ok(), "this cell does not mint personal tokens");
    const token = (await tok.json()).plaintext;
    const call = async (name: string, args: Record<string, unknown> = {}) => {
      const r = await page.request.post("/api/v1/mcp", {
        headers: { Authorization: `Bearer ${token}` },
        data: { jsonrpc: "2.0", id: 1, method: "tools/call", params: { name, arguments: args } },
      });
      expect(r.ok()).toBeTruthy();
      return (await r.json()).result;
    };
    const listed = await call("sluicio_list_collectors");
    expect(listed.isError ?? false).toBeFalsy();
    const ids = (listed.structuredContent?.collectors ?? []).map((c: { id: string }) => c.id);
    const mine = (await (await page.request.get("/api/v1/collectors")).json()).collectors.find(
      (c: { description: Record<string, string> }) => c.description["host.name"] === `e2e-a-${stamp}-0`,
    );
    expect(ids).toContain(mine.id);
    const got = await call("sluicio_get_collector", { id: mine.id });
    expect(JSON.stringify(got)).not.toContain(AGENT_SECRET);
  });
});
