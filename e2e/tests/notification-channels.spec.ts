// SPDX-License-Identifier: Apache-2.0
//
// Notification channels — every creatable kind actually delivers.
// Exercises the REAL delivery path via POST /notification-channels/{id}/test:
// webhook, Slack, and PagerDuty against a local HTTP sink (PagerDuty via
// its config.events_url override — also how EU-region accounts point at
// events.eu.pagerduty.com), email against an SMTP catcher (mailpit) when
// one is configured.
//
// Environment:
//   E2E_SINK_HOST     host where CELL-API reaches this process's sink
//                     (default localhost; host.containers.internal for compose)
//   E2E_SMTP_HOST     SMTP catcher host as cell-api sees it (skip email test if unset)
//   E2E_SMTP_PORT     SMTP catcher port (default 1025)
//   E2E_MAILPIT_API   mailpit API base as THIS process sees it (e.g. http://localhost:8025)
import http from "node:http";
import { test, expect, type APIRequestContext } from "@playwright/test";
import { logIn } from "./fixtures";

const SINK_HOST = process.env.E2E_SINK_HOST || "localhost";
const SMTP_HOST = process.env.E2E_SMTP_HOST || "";
const SMTP_PORT = process.env.E2E_SMTP_PORT || "1025";
const MAILPIT_API = process.env.E2E_MAILPIT_API || "";

type SinkHit = { path: string; body: string };
const hits: SinkHit[] = [];
let sink: http.Server;
let sinkPort = 0;

let seq = 0;
async function makeChannel(admin: APIRequestContext, kind: string, config: Record<string, string>): Promise<string> {
  // Counter suffix: two channels created within the same millisecond
  // must not collide on the org-unique name.
  const res = await admin.post("/api/v1/notification-channels", {
    data: { name: `e2e-${kind}-${Date.now().toString(36)}-${seq++}`, kind, config },
  });
  expect(res.ok(), `create ${kind} channel: ${res.status()}`).toBeTruthy();
  return (await res.json()).id;
}

async function testChannel(admin: APIRequestContext, id: string) {
  return admin.post(`/api/v1/notification-channels/${id}/test`);
}

test.describe("Notification channels — live delivery per kind", () => {
  const created: string[] = [];

  test.beforeAll(async () => {
    sink = http.createServer((req, res) => {
      let body = "";
      req.on("data", (c) => (body += c));
      req.on("end", () => {
        hits.push({ path: req.url ?? "", body });
        res.writeHead(202, { "Content-Type": "application/json" });
        res.end('{"ok":true}');
      });
    });
    await new Promise<void>((resolve) => sink.listen(0, "0.0.0.0", () => resolve()));
    sinkPort = (sink.address() as { port: number }).port;
  });

  test.afterAll(async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    await logIn(page);
    for (const id of created) await page.request.delete(`/api/v1/notification-channels/${id}`);
    await page.context().close();
    await new Promise<void>((resolve) => sink.close(() => resolve()));
  });

  test("webhook: canonical JSON lands with state, summary, and source", async ({ page }) => {
    await logIn(page);
    const id = await makeChannel(page.request, "webhook", { url: `http://${SINK_HOST}:${sinkPort}/ch-webhook` });
    created.push(id);
    expect((await testChannel(page.request, id)).ok()).toBeTruthy();
    const hit = hits.find((h) => h.path === "/ch-webhook");
    expect(hit).toBeTruthy();
    // Webhook channels receive the canonical content-toggled payload:
    // state / severity / summary / rule / link / sent_at / source.
    const body = JSON.parse(hit!.body);
    expect(body.source).toBe("sluicio");
    expect(body.state).toBe("firing");
    expect(String(body.summary)).toContain("Test notification");
    expect(body).toHaveProperty("sent_at");
  });

  test("slack: incoming-webhook text with state prefix and severity icon", async ({ page }) => {
    await logIn(page);
    const id = await makeChannel(page.request, "slack", { url: `http://${SINK_HOST}:${sinkPort}/ch-slack` });
    created.push(id);
    expect((await testChannel(page.request, id)).ok()).toBeTruthy();
    const hit = hits.find((h) => h.path === "/ch-slack");
    expect(hit).toBeTruthy();
    const body = JSON.parse(hit!.body);
    expect(String(body.text)).toContain("*[FIRING]*");
    expect(String(body.text)).toContain("Test notification");
  });

  test("pagerduty: Events API v2 trigger with routing key (events_url override)", async ({ page }) => {
    await logIn(page);
    const id = await makeChannel(page.request, "pagerduty", {
      routing_key: "R0E2ETESTKEY",
      events_url: `http://${SINK_HOST}:${sinkPort}/ch-pd`,
    });
    created.push(id);
    expect((await testChannel(page.request, id)).ok()).toBeTruthy();
    const hit = hits.find((h) => h.path === "/ch-pd");
    expect(hit).toBeTruthy();
    const body = JSON.parse(hit!.body);
    expect(body.routing_key).toBe("R0E2ETESTKEY");
    expect(body.event_action).toBe("trigger");
    expect(body.payload?.source).toBe("sluicio");
    expect(body.payload?.severity).toBeTruthy();
  });

  test("pagerduty: events_url must be http(s) when set", async ({ page }) => {
    await logIn(page);
    const res = await page.request.post("/api/v1/notification-channels", {
      data: { name: "e2e-pd-bad", kind: "pagerduty", config: { routing_key: "x", events_url: "ftp://nope" } },
    });
    expect(res.status()).toBe(400);
  });

  test("email: SMTP delivery with multipart body arrives at the catcher", async ({ page, request }) => {
    test.skip(!SMTP_HOST || !MAILPIT_API, "no SMTP catcher configured (E2E_SMTP_HOST + E2E_MAILPIT_API)");
    await logIn(page);
    const rcpt = `e2e-${Date.now().toString(36)}@test.local`;
    const id = await makeChannel(page.request, "email", {
      to: rcpt,
      smtp_host: SMTP_HOST,
      smtp_port: SMTP_PORT,
      from: "alerts@e2e.local",
    });
    created.push(id);
    expect((await testChannel(page.request, id)).ok()).toBeTruthy();

    // The catcher should hold exactly our message, addressed correctly,
    // with the standard subject shape.
    let found: { Subject: string } | undefined;
    for (let i = 0; i < 10 && !found; i++) {
      const inbox = await (await request.get(`${MAILPIT_API}/api/v1/search?query=to:${encodeURIComponent(rcpt)}`)).json();
      found = (inbox.messages ?? [])[0];
      if (!found) await new Promise((r) => setTimeout(r, 1000));
    }
    expect(found, "email did not arrive at the SMTP catcher").toBeTruthy();
    expect(found!.Subject).toContain("[FIRING]");
    expect(found!.Subject).toContain("Test notification");
  });

  test("unreachable destination surfaces a delivery error, not a false success", async ({ page }) => {
    await logIn(page);
    const id = await makeChannel(page.request, "webhook", { url: "http://127.0.0.1:9/black-hole" });
    created.push(id);
    const res = await testChannel(page.request, id);
    expect(res.ok()).toBeFalsy();
  });
});
