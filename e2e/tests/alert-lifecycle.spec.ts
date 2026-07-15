// SPDX-License-Identifier: Apache-2.0
//
// Alert lifecycle — the executable answer to: "service A shows an error
// on an integration: WHEN does an alert go out, to WHOM, and how is
// that configured?"
//
// The cadences under test (cell-api defaults):
//   - alert engine: evaluates enabled rules every 30s (ALERT_EVAL_INTERVAL)
//   - deliveries:   queued per (firing, channel), posted within ~5s
//                   (ALERT_DELIVERY_POLL), up to 5 attempts
//   - error notifier ("Errors detected on <service>"): every 60s
//     (ERROR_NOTIFY_INTERVAL), routed via the integration's notification
//     profile (falling back to the org default profile), and re-pages
//     only after the error is acknowledged and re-opens
//
// So: an error trace ingested at T fires a threshold rule by T+~30s and
// the webhook lands by T+~35s. The tests assert the full path by running
// a real HTTP sink and pointing webhook channels at it — the sink
// receiving the POST is the proof of "when" and "to whom".
//
// Environment:
//   E2E_INGEST_URL  OTLP/HTTP base of cell-ingest  (default http://localhost:4318)
//   E2E_SINK_HOST   host where CELL-API can reach THIS process's sink
//                   (default localhost; use host.containers.internal when
//                   cell-api runs in a container)
import http from "node:http";
import { test, expect, type APIRequestContext } from "@playwright/test";
import { logIn } from "./fixtures";
import { encodeTraceExport } from "./otlp";

const INGEST_URL = process.env.E2E_INGEST_URL || "http://localhost:4318";
const SINK_HOST = process.env.E2E_SINK_HOST || "localhost";

// ── the webhook sink ─────────────────────────────────────────────────────
type SinkHit = { at: number; path: string; body: string };
const hits: SinkHit[] = [];
let sink: http.Server;
let sinkPort = 0;

function sinkURL(path: string): string {
  return `http://${SINK_HOST}:${sinkPort}${path}`;
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

// ── admin-side fixtures ──────────────────────────────────────────────────
async function mintIngestKey(admin: APIRequestContext): Promise<string | null> {
  const res = await admin.post("/api/v1/ingest-keys", { data: { name: `e2e-alerts-${Date.now().toString(36)}` } });
  if (!res.ok()) return null;
  return (await res.json()).key ?? null;
}

async function ingestErrors(admin: APIRequestContext, key: string, service: string, count: number): Promise<boolean> {
  // cell-ingest accepts protobuf OTLP only — encoded by tests/otlp.ts.
  const body = encodeTraceExport(
    service,
    Array.from({ length: count }, () => ({
      name: "POST /e2e/fail",
      error: true,
      attrs: { "http.response.status_code": 500, "http.route": "/e2e/fail" },
    })),
  );
  const res = await admin.post(`${INGEST_URL}/v1/traces`, {
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/x-protobuf" },
    data: body,
    failOnStatusCode: false,
  });
  return res.ok();
}

async function makeChannel(admin: APIRequestContext, name: string, path: string): Promise<string> {
  const res = await admin.post("/api/v1/notification-channels", {
    data: { name, kind: "webhook", config: { url: sinkURL(path) } },
  });
  expect(res.ok()).toBeTruthy();
  return (await res.json()).id;
}

test.describe("Alert lifecycle — when, to whom, and how it's configured", () => {
  test.describe.configure({ mode: "serial" });
  const stamp = Date.now().toString(36);
  const cleanup: { rules: string[]; channels: string[]; integrations: string[]; profiles: string[]; groups: string[] } = {
    rules: [],
    channels: [],
    integrations: [],
    profiles: [],
    groups: [],
  };
  let ingestKey: string | null = null;

  test.beforeAll(async () => {
    sink = http.createServer((req, res) => {
      let body = "";
      req.on("data", (c) => (body += c));
      req.on("end", () => {
        hits.push({ at: Date.now(), path: req.url ?? "", body });
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end('{"ok":true}');
      });
    });
    await new Promise<void>((resolve) => sink.listen(0, "0.0.0.0", () => resolve()));
    sinkPort = (sink.address() as { port: number }).port;
  });

  test.afterAll(async ({ browser }) => {
    const page = await (await browser.newContext()).newPage();
    await logIn(page);
    const admin = page.request;
    for (const id of cleanup.rules) await admin.delete(`/api/v1/alert-rules/${id}`);
    for (const id of cleanup.integrations) await admin.delete(`/api/v1/integrations/${id}`);
    for (const id of cleanup.profiles) await admin.delete(`/api/v1/notification-profiles/${id}`);
    for (const id of cleanup.channels) await admin.delete(`/api/v1/notification-channels/${id}`);
    for (const id of cleanup.groups) await admin.delete(`/api/v1/settings/groups/${id}`);
    await page.context().close();
    await new Promise<void>((resolve) => sink.close(() => resolve()));
  });

  test("error traces on an integration fire a threshold rule within one engine tick, delivered ONLY to the rule's channels", async ({ page }) => {
    test.setTimeout(180_000);
    await logIn(page);
    const admin = page.request;
    ingestKey = await mintIngestKey(admin);
    test.skip(!ingestKey, "cannot mint an ingest key on this cell");

    // Service A, erroring inside an integration.
    const svc = `e2e-alert-svc-${stamp}`;
    const mk = await admin.post("/api/v1/integrations", {
      data: { slug: `e2e-alert-integ-${stamp}`, name: `E2E Alert Integ ${stamp}`, matchers: [{ operator: "equals", value: svc }] },
    });
    const integID = (await mk.json()).integration.id;
    cleanup.integrations.push(integID);

    const sent = await ingestErrors(admin, ingestKey!, svc, 3);
    test.skip(!sent, `cell-ingest not reachable at ${INGEST_URL}`);

    // Recipients: the rule routes to channel A. Channel B exists but is
    // NOT bound — it must stay silent (that's the "to whom" contract).
    const chBound = await makeChannel(admin, `e2e-bound-${stamp}`, "/hook-bound");
    const chUnbound = await makeChannel(admin, `e2e-unbound-${stamp}`, "/hook-unbound");
    cleanup.channels.push(chBound, chUnbound);

    const ruleRes = await admin.post("/api/v1/alert-rules", {
      data: {
        name: `e2e failed traces ${stamp}`,
        severity: "critical",
        enabled: true,
        signal: "trace",
        service_name: svc,
        integration_id: integID,
        evaluation_seconds: 30,
        trace_error_spec: { threshold: 1, window_seconds: 900 },
        channel_ids: [chBound],
      },
    });
    expect(ruleRes.status()).toBe(201);
    const rule = await ruleRes.json();
    const ruleID = rule.id ?? rule.rule?.id;
    cleanup.rules.push(ruleID);
    const armedAt = Date.now();

    // WHEN: the engine evaluates every 30s — the instance must be firing
    // within ~2 ticks, and the webhook lands within delivery-poll (~5s)
    // after that.
    let firing: { alert_rule_id: string; state: string; summary: string } | null = null;
    while (Date.now() - armedAt < 90_000 && !firing) {
      const instances = (await (await admin.get("/api/v1/alert-instances?limit=100")).json()).instances ?? [];
      firing = instances.find(
        (i: { alert_rule_id: string; state: string }) => i.alert_rule_id === ruleID && i.state === "firing",
      );
      if (!firing) await new Promise((r) => setTimeout(r, 3000));
    }
    expect(firing, "rule did not fire within 90s (3 engine ticks)").toBeTruthy();
    expect(firing!.summary).toContain("failed trace");

    const hit = await waitForHit("/hook-bound", 30_000);
    expect(hit, "webhook delivery did not arrive within 30s of firing").toBeTruthy();
    // The payload names the rule — a receiver can route on it.
    expect(hit!.body).toContain(`e2e failed traces ${stamp}`);
    // Total end-to-end budget: ingest → firing → delivered ≪ 2 minutes.
    expect(hit!.at - armedAt).toBeLessThan(120_000);
    // The unbound channel stayed silent.
    expect(hits.find((h) => h.path.startsWith("/hook-unbound"))).toBeFalsy();

    // The delivery ledger records the send (audit trail for "to whom").
    const deliveries = await (await admin.get("/api/v1/alert-deliveries?range=1h")).json();
    expect(JSON.stringify(deliveries)).toContain(`e2e failed traces ${stamp}`);
  });

  test("the error notifier pages the integration's notification-profile channels", async ({ page }) => {
    test.setTimeout(180_000);
    await logIn(page);
    const admin = page.request;
    test.skip(!ingestKey, "cannot mint an ingest key on this cell");

    // A second erroring service/integration, routed via a notification
    // PROFILE (the per-integration recipient config) instead of a rule.
    const svc = `e2e-notify-svc-${stamp}`;
    const mk = await admin.post("/api/v1/integrations", {
      data: { slug: `e2e-notify-integ-${stamp}`, name: `E2E Notify Integ ${stamp}`, matchers: [{ operator: "equals", value: svc }] },
    });
    const integID = (await mk.json()).integration.id;
    cleanup.integrations.push(integID);

    const ch = await makeChannel(admin, `e2e-profile-${stamp}`, "/hook-profile");
    cleanup.channels.push(ch);
    const profRes = await admin.post("/api/v1/notification-profiles", {
      data: { name: `e2e-profile-${stamp}` },
    });
    expect(profRes.ok()).toBeTruthy();
    const profID = (await profRes.json()).id;
    cleanup.profiles.push(profID);
    expect((await admin.put(`/api/v1/notification-profiles/${profID}/channels`, { data: { channel_ids: [ch] } })).ok()).toBeTruthy();
    expect((await admin.put(`/api/v1/integrations/${integID}/notification-profile`, { data: { profile_id: profID } })).ok()).toBeTruthy();

    const sent = await ingestErrors(admin, ingestKey!, svc, 2);
    test.skip(!sent, `cell-ingest not reachable at ${INGEST_URL}`);

    // WHEN: the notifier scans every 60s; one page per open error batch.
    const hit = await waitForHit("/hook-profile", 100_000);
    expect(hit, "error notification did not arrive within 100s (one notifier tick + margin)").toBeTruthy();
    expect(hit!.body).toContain(`Errors detected on ${svc}`);
  });

  test("recipient + trigger configuration round-trips; team-owned rules stay team-private", async ({ page, browser }) => {
    await logIn(page);
    const admin = page.request;

    // HOW it's configured: threshold, window, resolve mode, and the
    // channel binding are all rule fields an editor can change.
    const ch = await makeChannel(admin, `e2e-cfg-${stamp}`, "/hook-cfg");
    cleanup.channels.push(ch);
    const mk = await admin.post("/api/v1/alert-rules", {
      data: {
        name: `e2e cfg rule ${stamp}`,
        severity: "warning",
        enabled: false, // config-only — never fires
        signal: "trace",
        service_name: `e2e-cfg-svc-${stamp}`,
        trace_error_spec: { threshold: 5, window_seconds: 600 },
        resolve_mode: "manual",
        channel_ids: [ch],
      },
    });
    expect(mk.status()).toBe(201);
    const rule = await mk.json();
    const ruleID = rule.id ?? rule.rule?.id;
    cleanup.rules.push(ruleID);
    const spec = rule.trace_error_spec ?? rule.rule?.trace_error_spec;
    expect(spec.threshold).toBe(5);
    expect(spec.window_seconds).toBe(600);
    expect(rule.resolve_mode ?? rule.rule?.resolve_mode).toBe("manual");
    expect(rule.channel_ids ?? rule.rule?.channel_ids).toEqual([ch]);

    // Team ownership gates who even SEES the rule: a group-less viewer
    // must not learn that a team's alerting exists.
    const gid = (
      await (
        await admin.post("/api/v1/settings/groups", { data: { slug: `e2e-alert-team-${stamp}`, name: "E2E Alert Team" } })
      ).json()
    ).id;
    cleanup.groups.push(gid);
    const teamRule = await admin.post("/api/v1/alert-rules", {
      data: {
        name: `e2e team rule ${stamp}`,
        severity: "warning",
        enabled: false,
        signal: "trace",
        service_name: `e2e-cfg-svc-${stamp}`,
        group_id: gid,
        trace_error_spec: { threshold: 1, window_seconds: 300 },
        channel_ids: [],
      },
    });
    expect(teamRule.status()).toBe(201);
    const teamRuleBody = await teamRule.json();
    const teamRuleID = teamRuleBody.id ?? teamRuleBody.rule?.id;
    cleanup.rules.push(teamRuleID);

    await admin.post("/api/v1/settings/members", {
      data: { email: "e2e-alert-viewer@sluicio.local", name: "E2E Alert Viewer", password: "e2e-alert-viewer-pw1", role: "viewer" },
    });
    const viewer = await (await browser.newContext()).newPage();
    await logIn(viewer, "e2e-alert-viewer@sluicio.local", "e2e-alert-viewer-pw1");
    const rules = (await (await viewer.request.get("/api/v1/alert-rules")).json()).rules ?? [];
    expect(rules.map((r: { id: string }) => r.id)).not.toContain(teamRuleID);
    await viewer.context().close();
  });
});
