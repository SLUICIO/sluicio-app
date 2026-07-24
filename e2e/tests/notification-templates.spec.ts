// SPDX-License-Identifier: Apache-2.0
//
// Notification message templates (issue #5): format gets routing's
// org→team ladder. Covered here:
//   - API contract: org set round-trips; malformed Liquid → 400 naming
//     the field; a viewer cannot PUT a team's override (403)
//   - the reflected variable palette (documented, carries the contract
//     paths) and the Slack preview
//   - the real ladder: an org-default Slack template set via the API
//     renders in an actual fired alert delivered to a Slack channel
//     (HTTP sink is the proof), then cleanup restores the built-in line
//   - UI smoke: the org card on Settings → System and the team card in
//     the group drawer
import http from "node:http";
import { test, expect, type APIRequestContext, request as pwRequest } from "@playwright/test";
import { logIn } from "./fixtures";
import { encodeTraceExport } from "./otlp";

const INGEST_URL = process.env.E2E_INGEST_URL || "http://localhost:4318";
const SINK_HOST = process.env.E2E_SINK_HOST || "localhost";
const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";

const EMPTY_SET = { email_subject: "", email_body: "", slack_title: "", slack_body: "" };

test.describe("Notification message templates", () => {
  test.describe.configure({ mode: "serial" });

  const stamp = Date.now().toString(36);
  type SinkHit = { path: string; body: string };
  const hits: SinkHit[] = [];
  let sink: http.Server;
  let sinkPort = 0;

  test.beforeAll(async () => {
    sink = http.createServer((req, res) => {
      let body = "";
      req.on("data", (c) => (body += c));
      req.on("end", () => {
        hits.push({ path: req.url ?? "", body });
        res.writeHead(200).end("ok");
      });
    });
    await new Promise<void>((r) => sink.listen(0, () => r()));
    sinkPort = (sink.address() as { port: number }).port;
  });
  test.afterAll(async () => {
    await new Promise<void>((r) => sink.close(() => r()));
  });

  test("org set round-trips; malformed Liquid is a 400 naming the field", async ({ page }) => {
    await logIn(page);
    const admin = page.request;
    const put = await admin.put("/api/v1/settings/notification-templates", {
      data: { ...EMPTY_SET, slack_body: "{{ alert.summary }}" },
    });
    expect(put.ok()).toBeTruthy();
    const got = await (await admin.get("/api/v1/settings/notification-templates")).json();
    expect(got.slack_body).toBe("{{ alert.summary }}");

    const bad = await admin.put("/api/v1/settings/notification-templates", {
      data: { ...EMPTY_SET, slack_body: "{% if x %}unclosed" },
    });
    expect(bad.status()).toBe(400);
    expect(JSON.stringify(await bad.json())).toContain("slack_body");

    // Restore the pristine state for the rest of the suite.
    expect((await admin.put("/api/v1/settings/notification-templates", { data: EMPTY_SET })).ok()).toBeTruthy();
  });

  test("a viewer cannot PUT a team's override; the palette carries the contract", async ({ page }) => {
    await logIn(page);
    const admin = page.request;
    // Any group will do as the target.
    const groups = (await (await admin.get("/api/v1/settings/groups")).json()).groups ?? [];
    test.skip(groups.length === 0, "no groups on this cell");
    const gid = groups[0].id;

    // Provision a fixed viewer (idempotent — 409 = exists from a past run).
    const email = "e2e-tmpl-viewer@sluicio.local";
    const password = "e2e-tmpl-viewer-pw1";
    const mk = await admin.post("/api/v1/settings/members", {
      data: { email, name: "E2E Tmpl Viewer", password, role: "viewer" },
    });
    expect(mk.ok() || mk.status() === 409).toBeTruthy();
    const viewer = await pwRequest.newContext({ baseURL: BASE_URL });
    expect((await viewer.post("/api/v1/auth/login", { data: { email, password } })).ok()).toBeTruthy();
    const denied = await viewer.put(`/api/v1/settings/groups/${gid}/notification-template`, {
      data: { ...EMPTY_SET, slack_body: "nope" },
    });
    expect(denied.status()).toBe(403);
    await viewer.dispose();

    // The reflected palette: documented entries incl. the public paths.
    const schema = await (await admin.get("/api/v1/alerting/template-context-schema")).json();
    const paths = (schema.variables ?? []).map((v: { path: string }) => v.path);
    for (const must of ["alert.state_emoji", "alert.severity", "check.value", "service.metadata.<key>"]) {
      expect(paths).toContain(must);
    }
    for (const v of schema.variables ?? []) {
      expect(v.description, `${v.path} undocumented`).toBeTruthy();
    }

    // Slack preview renders the candidate template against the sample.
    const prev = await (
      await admin.post("/api/v1/alert-templates/preview", {
        data: { kind: "slack", content: { slack_title: "{{ alert.state_emoji }} {{ rule.name }}", slack_body: "{{ alert.summary }}" } },
      })
    ).json();
    expect(prev.body).toContain(":red_circle:");
    expect(prev.body).toContain("Checkout error rate");
  });

  test("an org-default Slack template renders in a real fired alert", async ({ page }) => {
    test.setTimeout(180_000);
    await logIn(page);
    const admin = page.request;

    const keyRes = await admin.post("/api/v1/ingest-keys", { data: { name: `e2e-tmpl-${stamp}` } });
    test.skip(!keyRes.ok(), "cannot mint an ingest key on this cell");
    const ingestKey = (await keyRes.json()).key;

    const svc = `e2e-tmpl-svc-${stamp}`;
    const sent = await admin.post(`${INGEST_URL}/v1/traces`, {
      headers: { Authorization: `Bearer ${ingestKey}`, "Content-Type": "application/x-protobuf" },
      data: encodeTraceExport(svc, [{ name: "POST /e2e/fail", error: true, attrs: { "http.response.status_code": 500 } }]),
      failOnStatusCode: false,
    });
    test.skip(!sent.ok(), `cell-ingest not reachable at ${INGEST_URL}`);

    // Org-default Slack template — the rule carries NO inline override,
    // so the delivered text proves the stored-ladder rung applied.
    expect(
      (
        await admin.put("/api/v1/settings/notification-templates", {
          data: { ...EMPTY_SET, slack_title: "TMPL {{ rule.name }}", slack_body: "sev={{ alert.severity }} {{ alert.summary }}" },
        })
      ).ok(),
    ).toBeTruthy();

    const ch = await admin.post("/api/v1/notification-channels", {
      data: { name: `e2e-tmpl-slack-${stamp}`, kind: "slack", config: { url: `http://${SINK_HOST}:${sinkPort}/tmpl-slack` } },
    });
    expect(ch.ok()).toBeTruthy();
    const chID = (await ch.json()).id;
    const rule = await admin.post("/api/v1/alert-rules", {
      data: {
        name: `e2e tmpl rule ${stamp}`,
        severity: "critical",
        enabled: true,
        signal: "trace",
        service_name: svc,
        evaluation_seconds: 30,
        trace_error_spec: { threshold: 1, window_seconds: 900 },
        channel_ids: [chID],
      },
    });
    expect(rule.status()).toBe(201);
    const ruleBody = await rule.json();
    const ruleID = ruleBody.id ?? ruleBody.rule?.id;

    try {
      const deadline = Date.now() + 120_000;
      let hit: SinkHit | undefined;
      while (Date.now() < deadline && !hit) {
        hit = hits.find((h) => h.path.startsWith("/tmpl-slack"));
        if (!hit) await new Promise((r) => setTimeout(r, 3000));
      }
      expect(hit, "no slack delivery within 120s").toBeTruthy();
      const text = String(JSON.parse(hit!.body).text);
      // The ladder-rendered shape: bold title line + templated body —
      // not the built-in "icon *[FIRING]* summary" line.
      expect(text).toContain(`*TMPL e2e tmpl rule ${stamp}*`);
      expect(text).toContain("sev=critical");
      expect(text).not.toContain("*[FIRING]*");
    } finally {
      await admin.put("/api/v1/settings/notification-templates", { data: EMPTY_SET });
      if (ruleID) await admin.delete(`/api/v1/alert-rules/${ruleID}`);
      await admin.delete(`/api/v1/notification-channels/${chID}`);
    }
  });

  test("UI: org card on Settings → System; team card in the group drawer", async ({ page }) => {
    await logIn(page);
    await page.goto("/settings?tab=system");
    await expect(page.getByRole("heading", { name: "Notification templates" })).toBeVisible();
    await expect(page.getByText("Slack body (mrkdwn)").first()).toBeVisible();

    const groups = (await (await page.request.get("/api/v1/settings/groups")).json()).groups ?? [];
    test.skip(groups.length === 0, "no groups on this cell");
    await page.goto("/settings?tab=groups");
    await page.getByText(groups[0].name, { exact: true }).first().click();
    await expect(page.getByRole("heading", { name: "Notification templates" })).toBeVisible();
  });
});
