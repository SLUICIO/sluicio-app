// SPDX-License-Identifier: Apache-2.0
//
// Outbound event subscriptions (issue #4): domain events fan out to
// webhook channels through filter globs. Proven live against an HTTP
// sink: a config mutation (integration.created) arrives in the
// canonical shape on one subscription and as a CloudEvents 1.0 envelope
// on a CE-format channel; filters exclude non-matching families; a
// viewer cannot create an org-wide subscription; the Developers page
// shows the manager.
import http from "node:http";
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { logIn, ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";
const SINK_HOST = process.env.E2E_SINK_HOST || "localhost";

test.describe("Event subscriptions", () => {
  test.describe.configure({ mode: "serial" });

  const stamp = Date.now().toString(36);
  type SinkHit = { path: string; body: string; contentType: string };
  const hits: SinkHit[] = [];
  let sink: http.Server;
  let sinkPort = 0;
  const cleanup = { subs: [] as string[], channels: [] as string[], integrations: [] as string[] };

  test.beforeAll(async () => {
    sink = http.createServer((req, res) => {
      let body = "";
      req.on("data", (c) => (body += c));
      req.on("end", () => {
        hits.push({ path: req.url ?? "", body, contentType: String(req.headers["content-type"] ?? "") });
        res.writeHead(200).end("ok");
      });
    });
    await new Promise<void>((r) => sink.listen(0, () => r()));
    sinkPort = (sink.address() as { port: number }).port;
  });

  test.afterAll(async () => {
    const admin = await pwRequest.newContext({ baseURL: BASE_URL });
    await admin.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
    for (const id of cleanup.subs) await admin.delete(`/api/v1/event-subscriptions/${id}`);
    for (const id of cleanup.integrations) await admin.delete(`/api/v1/integrations/${id}`);
    for (const id of cleanup.channels) await admin.delete(`/api/v1/notification-channels/${id}`);
    await admin.dispose();
    await new Promise<void>((r) => sink.close(() => r()));
  });

  async function makeChannel(admin: APIRequestContext, name: string, path: string, format?: string): Promise<string> {
    const config: Record<string, string> = { url: `http://${SINK_HOST}:${sinkPort}${path}` };
    if (format) config.format = format;
    const res = await admin.post("/api/v1/notification-channels", { data: { name, kind: "webhook", config } });
    expect(res.ok()).toBeTruthy();
    const id = (await res.json()).id;
    cleanup.channels.push(id);
    return id;
  }

  async function waitForHit(pathPrefix: string, timeoutMs: number): Promise<SinkHit | null> {
    const deadline = Date.now() + timeoutMs;
    while (Date.now() < deadline) {
      const hit = hits.find((h) => h.path.startsWith(pathPrefix));
      if (hit) return hit;
      await new Promise((r) => setTimeout(r, 1500));
    }
    return null;
  }

  test("a config mutation reaches both payload shapes; filters exclude other families", async ({ page }) => {
    test.setTimeout(120_000);
    await logIn(page);
    const admin = page.request;

    // Two subscriptions: canonical JSON on integration.*, CloudEvents on *.
    const chPlain = await makeChannel(admin, `e2e-ev-plain-${stamp}`, "/ev-plain");
    const chCE = await makeChannel(admin, `e2e-ev-ce-${stamp}`, "/ev-ce", "cloudevents");
    for (const [name, channel_id, filters] of [
      [`e2e-sub-plain-${stamp}`, chPlain, ["com.sluicio.integration.*"]],
      [`e2e-sub-ce-${stamp}`, chCE, ["*"]],
    ] as const) {
      const res = await admin.post("/api/v1/event-subscriptions", {
        data: { name, channel_id, event_filters: [...filters] },
      });
      expect(res.status()).toBe(201);
      cleanup.subs.push((await res.json()).id);
    }

    // The triggering mutation.
    const mk = await admin.post("/api/v1/integrations", {
      data: { slug: `e2e-ev-integ-${stamp}`, name: `E2E Event Integ ${stamp}`, matchers: [{ operator: "equals", value: `e2e-ev-svc-${stamp}` }] },
    });
    expect(mk.status()).toBe(201);
    cleanup.integrations.push((await mk.json()).integration.id);

    // Canonical shape on the integration.* subscription.
    const plain = await waitForHit("/ev-plain", 30_000);
    expect(plain, "canonical event not delivered within 30s").toBeTruthy();
    const pBody = JSON.parse(plain!.body);
    expect(pBody.event).toBe("com.sluicio.integration.created");
    expect(pBody.source).toBe("sluicio");
    expect(pBody.id).toBeTruthy();
    expect(pBody.data.target_type).toBe("integration");

    // CloudEvents envelope on the CE channel. The * subscription also
    // receives its own creation event (subscription CRUD is audited and
    // audits emit) — wait for the integration.created hit specifically.
    const ce = await (async () => {
      const deadline = Date.now() + 30_000;
      while (Date.now() < deadline) {
        const hit = hits.find((h) => h.path.startsWith("/ev-ce") && h.body.includes("com.sluicio.integration.created"));
        if (hit) return hit;
        await new Promise((r) => setTimeout(r, 1500));
      }
      return null;
    })();
    expect(ce, "CE event not delivered within 30s").toBeTruthy();
    expect(ce!.contentType).toContain("application/cloudevents+json");
    const ceBody = JSON.parse(ce!.body);
    expect(ceBody.specversion).toBe("1.0");
    expect(ceBody.type).toBe("com.sluicio.integration.created");
    // Same emission → same event id across subscriptions (consumer dedup).
    expect(ceBody.id).toBe(pBody.id);

    // Filter exclusion: a GROUP mutation must reach the * subscription
    // but never the integration.* one. (No hit-COUNT assertions here —
    // parallel suite workers create integrations of their own, which
    // legitimately land on the integration.* subscription.)
    const g = await admin.post("/api/v1/settings/groups", {
      data: { name: `e2e-ev-group-${stamp}`, slug: `e2e-ev-group-${stamp}` },
    });
    expect(g.ok()).toBeTruthy();
    const gid = (await g.json()).id ?? (await g.json()).group?.id;
    const ceGroup = await (async () => {
      const deadline = Date.now() + 30_000;
      while (Date.now() < deadline) {
        const hit = hits.find((h) => h.path.startsWith("/ev-ce") && h.body.includes("com.sluicio.group.created"));
        if (hit) return hit;
        await new Promise((r) => setTimeout(r, 1500));
      }
      return null;
    })();
    expect(ceGroup, "group event not delivered to * subscription").toBeTruthy();
    // By now the worker sweep that delivered the group event to the *
    // subscription has run — a wrongly-matched plain delivery would have
    // arrived with it.
    const plainGroupHit = hits.find((h) => h.path.startsWith("/ev-plain") && h.body.includes("com.sluicio.group."));
    expect(plainGroupHit, "integration.* subscription must not receive group events").toBeFalsy();
    if (gid) await admin.delete(`/api/v1/settings/groups/${gid}`);
  });

  test("a viewer cannot create an org-wide subscription", async ({ page }) => {
    await logIn(page);
    const admin = page.request;
    const email = "e2e-ev-viewer@sluicio.local";
    const password = "e2e-ev-viewer-pw1";
    const mk = await admin.post("/api/v1/settings/members", {
      data: { email, name: "E2E Ev Viewer", password, role: "viewer" },
    });
    expect(mk.ok() || mk.status() === 409).toBeTruthy();
    const ch = await makeChannel(admin, `e2e-ev-deny-${stamp}`, "/ev-deny");

    const viewer = await pwRequest.newContext({ baseURL: BASE_URL });
    expect((await viewer.post("/api/v1/auth/login", { data: { email, password } })).ok()).toBeTruthy();
    const denied = await viewer.post("/api/v1/event-subscriptions", {
      data: { name: "nope", channel_id: ch, event_filters: ["*"] },
    });
    expect(denied.status()).toBe(403);
    await viewer.dispose();
  });

  test("Developers page shows the manager", async ({ page }) => {
    await logIn(page);
    await page.goto("/developers");
    await expect(page.getByRole("heading", { name: "Event subscriptions" })).toBeVisible();
    await expect(page.getByRole("button", { name: "+ New subscription" })).toBeVisible();
  });
});
