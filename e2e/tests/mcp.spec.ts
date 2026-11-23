// SPDX-License-Identifier: Apache-2.0
//
// The MCP surface as a remote connector meets it (issue #8, WS1):
// transport behaviour, and — the part no unit test can cover — whether
// the output schemas each tool ADVERTISES still describe what cell-api
// actually returns.
//
// That gap is the whole reason this spec exists. The Go tests check the
// schemas are well-formed against canned payloads; only a seeded cell
// can tell you that `services` is still called `services` and that
// `trace_count` is still a number. A schema that drifts is worse than no
// schema: a client is entitled to validate against it, so the first
// symptom of a renamed field is every agent call failing at once, with
// the error surfacing inside the client rather than here.
//
// It also pins the transport's one security property — that a
// cross-origin caller cannot ride the user's session cookie.

import { test, expect, type APIRequestContext } from "@playwright/test";
import crypto from "node:crypto";
import { logIn } from "./fixtures";
import { encodeTraceExport } from "./otlp";

const INGEST_URL = process.env.E2E_INGEST_URL || "http://localhost:4318";

type Schema = Record<string, any>;

/** One JSON-RPC round trip against the remote transport. */
async function rpc(
  request: APIRequestContext,
  token: string,
  method: string,
  params?: unknown,
) {
  const resp = await request.post("/api/v1/mcp", {
    headers: { Authorization: `Bearer ${token}` },
    data: { jsonrpc: "2.0", id: 1, method, params },
  });
  expect(resp.ok(), `${method}: HTTP ${resp.status()}`).toBeTruthy();
  return (await resp.json()).result;
}

async function callTool(
  request: APIRequestContext,
  token: string,
  name: string,
  args: Record<string, unknown> = {},
) {
  return rpc(request, token, "tools/call", { name, arguments: args });
}

function jsType(v: unknown): string {
  return Array.isArray(v) ? "array" : v === null ? "null" : typeof v;
}

/**
 * Validates a payload against the subset of JSON Schema these output
 * schemas use: type, properties, items, required.
 *
 * Absent fields are fine unless `required` names them — item fields are
 * conditional by design (`sample_trace_id` only when something errored),
 * so the check is "what IS here has the declared shape", not "everything
 * declared is here". Only the first few array items are inspected: a
 * rename shows up in the first one, and walking thousands of log records
 * would turn a guard into a slow test nobody keeps.
 */
function validate(path: string, schema: Schema, value: unknown, problems: string[]): void {
  if (value === null || value === undefined) return;
  switch (schema.type) {
    case "object": {
      if (jsType(value) !== "object") {
        problems.push(`${path}: schema says object, payload has ${jsType(value)}`);
        return;
      }
      for (const key of (schema.required ?? []) as string[]) {
        if (!(key in (value as object))) {
          problems.push(`${path}.${key}: required by the schema, absent from the payload`);
        }
      }
      for (const [key, sub] of Object.entries((schema.properties ?? {}) as Record<string, Schema>)) {
        if (key in (value as Record<string, unknown>)) {
          validate(`${path}.${key}`, sub, (value as Record<string, unknown>)[key], problems);
        }
      }
      return;
    }
    case "array": {
      if (!Array.isArray(value)) {
        problems.push(`${path}: schema says array, payload has ${jsType(value)}`);
        return;
      }
      if (schema.items) {
        value.slice(0, 5).forEach((item, i) => validate(`${path}[${i}]`, schema.items, item, problems));
      }
      return;
    }
    case "string":
    case "boolean":
      if (jsType(value) !== schema.type) {
        problems.push(`${path}: schema says ${schema.type}, payload has ${jsType(value)}`);
      }
      return;
    case "integer":
    case "number":
      if (typeof value !== "number") {
        problems.push(`${path}: schema says ${schema.type}, payload has ${jsType(value)}`);
      }
      return;
    default:
      return; // untyped (deliberately free-form)
  }
}

async function mintAdminToken(api: APIRequestContext): Promise<string> {
  const res = await api.post("/api/v1/settings/tokens", { data: { name: `e2e-mcp-ws1-${Date.now()}` } });
  expect(res.ok(), `mint token: ${res.status()}`).toBeTruthy();
  return (await res.json()).plaintext;
}

test.describe("MCP — declared output schemas match the cell", () => {
  test("the validator itself detects drift", async () => {
    // Without this, a bug that made validate() a no-op would turn the
    // conformance test below into a green light that checks nothing —
    // the failure mode a guard test is least able to notice about itself.
    const schema: Schema = {
      type: "object",
      required: ["services"],
      properties: {
        services: {
          type: "array",
          items: { type: "object", properties: { trace_count: { type: "integer" } } },
        },
        window: { type: "object", properties: { from: { type: "string" } } },
      },
    };

    let problems: string[] = [];
    validate("t", schema, { services: [{ trace_count: 4 }], window: { from: "x" } }, problems);
    expect(problems, "a conforming payload must produce no complaints").toEqual([]);

    // A renamed envelope key — the breaking change most likely to happen.
    problems = [];
    validate("t", schema, { svcs: [] }, problems);
    expect(problems.join(" ")).toContain("required by the schema");

    // A field that changed type underneath us.
    problems = [];
    validate("t", schema, { services: [{ trace_count: "4" }] }, problems);
    expect(problems.join(" ")).toContain("schema says integer");

    // An envelope that stopped being an object at all.
    problems = [];
    validate("t", schema, { services: { count: 1 } }, problems);
    expect(problems.join(" ")).toContain("schema says array");

    // Extra fields are explicitly fine: the API must stay free to grow.
    problems = [];
    validate("t", schema, { services: [{ trace_count: 1, brand_new: true }], extra: 1 }, problems);
    expect(problems, "a new cell-api field must not fail an agent's call").toEqual([]);
  });

  test("every tool declares an output schema and returns a payload that satisfies it", async ({ page, request }) => {
    await logIn(page);
    const admin = page.request;
    const token = await mintAdminToken(admin);

    const { tools } = await rpc(request, token, "tools/list");
    expect(tools.length, "the catalogue is empty").toBeGreaterThan(10);

    // Resolve the ids the id-taking tools need. These come from the
    // catalogue itself, so the spec exercises the same discovery path an
    // agent would: list, then drill in.
    const services = JSON.parse(
      (await callTool(request, token, "sluicio_list_services", { window: "24h" })).content[0].text,
    ).services;
    expect(services?.length, "the seeded cell has no services").toBeGreaterThan(0);

    const integrations = JSON.parse(
      (await callTool(request, token, "sluicio_list_integrations")).content[0].text,
    ).integrations;
    expect(integrations?.length, "the seeded cell has no integrations").toBeGreaterThan(0);

    // A system may not exist yet. Rather than skip the tool — a skipped
    // test is a test that stops catching drift — mark a service as one.
    let systems = JSON.parse(
      (await callTool(request, token, "sluicio_list_systems")).content[0].text,
    ).systems;
    let markedService: string | null = null;
    if (!systems?.length) {
      markedService = services[0].service_name;
      const marked = await admin.put(`/api/v1/services/${encodeURIComponent(markedService)}/system`, {
        data: { is_system: true, system_kind: "rabbitmq" },
      });
      expect(marked.ok(), `mark system: ${marked.status()}`).toBeTruthy();
      systems = JSON.parse(
        (await callTool(request, token, "sluicio_list_systems")).content[0].text,
      ).systems;
    }

    const traces = JSON.parse(
      (await callTool(request, token, "sluicio_search_traces", { window: "7d", errors_only: false, limit: 1 }))
        .content[0].text,
    ).results;
    expect(traces?.length, "the seeded cell has no traces to drill into").toBeGreaterThan(0);

    const metrics = JSON.parse(
      (await callTool(request, token, "sluicio_metric_catalog", { window: "7d" })).content[0].text,
    ).metrics;

    // A check of the test's own for the rule tools and the dry run: a
    // metric check whose threshold nothing reaches, so it never fires.
    const ruleResp = await admin.post("/api/v1/alert-rules", {
      data: {
        name: `e2e mcp schema ${Date.now().toString(36)}`, severity: "info", signal: "metric",
        spec: { metric_name: metrics?.[0]?.name ?? "queue.depth", aggregation: "avg", operator: "gt", threshold: 1e12, for_window: "5m" },
      },
    });
    expect(ruleResp.ok(), `create rule: ${ruleResp.status()}`).toBeTruthy();
    const ruleId = (await ruleResp.json()).id as string;

    const args: Record<string, Record<string, unknown>> = {
      sluicio_get_integration: { id: integrations[0].id },
      sluicio_get_system: { id: systems[0].id },
      sluicio_get_trace: { trace_id: traces[0].trace_id },
      sluicio_metric_series: { metric: metrics?.[0]?.name ?? "queue.depth", window: "7d" },
      sluicio_search_traces: { window: "7d", errors_only: false, limit: 5 },
      sluicio_search_logs: { window: "7d", limit: 5 },
      sluicio_get_alert_rule: { id: ruleId },
      sluicio_backtest_rule: { rule_id: ruleId, days: 1 },
      sluicio_list_completion_rules: { integration_id: integrations[0].id },
      sluicio_delayed_messages: { integration_id: integrations[0].id },
      // A draft whose spans no trace carries: it follows nothing, which
      // is still a payload the schema must describe.
      sluicio_dry_run_completion_rule: {
        integration_id: integrations[0].id,
        rule: { name: "e2e dry run", start_span_name: "e2e.start", stages: [{ spans: [{ span_name: "e2e.end" }], timeout_seconds: 60 }] },
        hours: 1,
      },
      sluicio_span_names: { integration_id: integrations[0].id },
      sluicio_metric_label_values: { key: "service.name", metric: metrics?.[0]?.name ?? "queue.depth" },
    };

    try {
      const problems: string[] = [];
      const gated: string[] = [];
      let checked = 0;
      for (const tool of tools) {
        expect(tool.outputSchema, `${tool.name} declares no outputSchema`).toBeTruthy();
        expect(tool.annotations, `${tool.name} has no annotations`).toBeTruthy();
        // The writer is exercised separately, below — calling it here
        // would leave a proposal in the queue on every run.
        if (tool.annotations.readOnlyHint !== true) continue;

        const result = await callTool(request, token, tool.name, args[tool.name] ?? { window: "24h" });

        // A tool behind an Enterprise entitlement returns 402 on a cell
        // without the licence. That is the gate working, not a schema
        // problem — this test's claim is "if a tool returns a payload,
        // the payload matches its declared schema", and a 402 is not a
        // payload. Matched on the machine-readable marker rather than
        // the status text, and ONLY that marker: every other error still
        // fails, and the `checked` floor below stops the whole test
        // decaying into skips if gating ever spread.
        if (result.isError && String(result.content?.[0]?.text ?? "").includes('"enterprise_feature"')) {
          gated.push(tool.name);
          continue;
        }
        expect(result.isError, `${tool.name}: ${result.content?.[0]?.text}`).toBeFalsy();
        expect(
          result.structuredContent,
          `${tool.name} declares an outputSchema but returned no structuredContent — a validating client rejects this call`,
        ).toBeTruthy();
        // The text block must still carry the same payload: clients
        // predating structured output read only that one.
        expect(JSON.parse(result.content[0].text)).toEqual(result.structuredContent);

        validate(tool.name, tool.outputSchema, result.structuredContent, problems);
        checked++;
      }
      if (gated.length > 0) {
        // Say what was not covered. A silently reduced scope reads as
        // "everything passed" when it was not everything.
        console.log(`  (entitlement-gated on this cell, not schema-checked: ${gated.join(", ")})`);
      }
      expect(checked, "no read-only tool was actually called").toBeGreaterThan(10);
      expect(problems, `declared output schemas no longer describe what cell-api returns:\n${problems.join("\n")}`)
        .toEqual([]);
    } finally {
      await admin.delete(`/api/v1/alert-rules/${ruleId}`).catch(() => {});
      if (markedService) {
        await admin
          .put(`/api/v1/services/${encodeURIComponent(markedService)}/system`, {
            data: { is_system: false, system_kind: "" },
          })
          .catch(() => {});
      }
    }
  });

  test("the propose tool's response matches its schema, and files a real proposal", async ({ page, request }) => {
    await logIn(page);
    const admin = page.request;
    const token = await mintAdminToken(admin);

    const created = await admin.post("/api/v1/alert-rules", {
      data: {
        name: `e2e-mcp-schema-${Date.now()}`,
        signal: "metric",
        severity: "warning",
        enabled: true,
        spec: { metric_name: "queue.depth", aggregation: "avg", operator: "gt", threshold: 5, for_window: "5m" },
      },
    });
    expect(created.ok(), `create rule: ${created.status()}`).toBeTruthy();
    const ruleId = (await created.json()).id;

    try {
      const { tools } = await rpc(request, token, "tools/list");
      const propose = tools.find((t: { name: string }) => t.name === "sluicio_propose_check_tuning");
      expect(propose, "the propose tool is missing from the catalogue").toBeTruthy();
      // The one tool that writes must say so, or a client skips the
      // confirmation a human is meant to see.
      expect(propose.annotations.readOnlyHint).toBe(false);

      const result = await callTool(request, token, "sluicio_propose_check_tuning", {
        rule_id: ruleId,
        rationale: "e2e: schema round-trip, not a real tuning suggestion",
        threshold: 9,
      });
      expect(result.isError, result.content?.[0]?.text).toBeFalsy();
      expect(result.structuredContent).toBeTruthy();

      const problems: string[] = [];
      validate("sluicio_propose_check_tuning", propose.outputSchema, result.structuredContent, problems);
      expect(problems, problems.join("\n")).toEqual([]);
      // Nothing has changed yet — that is the point of the primitive.
      expect(result.structuredContent.state).toBe("pending");

      // Clear the queue so the inbox badge doesn't accumulate across runs.
      await admin.post(`/api/v1/proposals/${result.structuredContent.id}/reject`, {
        data: { note: "e2e cleanup" },
      });
    } finally {
      await admin.delete(`/api/v1/alert-rules/${ruleId}`).catch(() => {});
    }
  });
});

// A question about one business object, asked the way people ask it:
// "everything that happened to invoice 123", where the telemetry calls
// the invoice iId and only the integration's Messages view calls it
// Invoice Id. Before this an agent could only page through the latest
// traces and read every span.
test.describe("MCP — trace questions by attribute, in the integration's words", () => {
  test("a column label finds the invoice, labels its values and brings its spans", async ({ page, request }) => {
    test.setTimeout(150_000);
    await logIn(page);
    const admin = page.request;
    const token = await mintAdminToken(admin);
    const stamp = Date.now().toString(36);
    const service = `e2e-invoice-${stamp}`;
    const invoice = `INV-${stamp}`;
    const traceId = crypto.randomBytes(16).toString("hex");

    const keyRes = await admin.post("/api/v1/ingest-keys", { data: { name: `e2e-invoice-${stamp}` }, failOnStatusCode: false });
    expect(keyRes.ok(), `could not mint an ingest key (${keyRes.status()})`).toBeTruthy();
    const key = (await keyRes.json()).key as string;
    // Two steps of one message carry the invoice; a second message
    // carries another one and must not be returned.
    const sent = await admin.post(`${INGEST_URL}/v1/traces`, {
      headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/x-protobuf" },
      data: encodeTraceExport(service, [
        { name: "invoice.receive", traceId, attrs: { "e2e.iId": invoice } },
        { name: "invoice.post", traceId, attrs: { "e2e.iId": invoice } },
        { name: "invoice.receive", attrs: { "e2e.iId": `${invoice}-other` } },
      ]),
      failOnStatusCode: false,
    });
    expect(sent.ok(), `ingest failed: ${sent.status()}`).toBeTruthy();

    const mk = await admin.post("/api/v1/integrations", {
      data: {
        slug: `invoices-${stamp}`,
        name: `Invoices ${stamp}`,
        matchers: [{ attribute: "service.name", operator: "equals", value: service }],
      },
    });
    expect(mk.ok(), `creating the integration failed: ${mk.status()}`).toBeTruthy();
    const integId = (await mk.json()).integration.id as string;

    try {
      const cols = await admin.put(`/api/v1/integrations/${integId}/message-columns`, {
        data: { columns: [{ kind: "attribute", key: "e2e.iId", label: "Invoice Id" }] },
      });
      expect(cols.ok(), `saving the columns failed: ${cols.status()}`).toBeTruthy();

      const { tools } = await rpc(request, token, "tools/list");
      const schemaOf = (name: string) => tools.find((t: { name: string }) => t.name === name).outputSchema;

      // Ingest lands asynchronously. Membership must not: the search
      // decides it from the matchers, and the spans read for the same
      // hit has to agree with it at once, which is why the poll stops at
      // the first hit and the spans are asserted without waiting.
      let found: any;
      await expect
        .poll(
          async () => {
            const res = await callTool(request, token, "sluicio_search_traces", {
              integration: `invoices-${stamp}`,
              attributes: [{ key: "Invoice Id", value: invoice }],
              include_spans: true,
              window: "1h",
            });
            found = res.structuredContent;
            return found?.results?.length ?? 0;
          },
          { timeout: 90_000 },
        )
        .toBe(1);

      expect(found.resolved_attributes).toEqual([{ given: "Invoice Id", key: "e2e.iId" }]);
      const hit = found.results[0];
      expect(hit.trace_id).toBe(traceId);
      expect(hit.columns?.["Invoice Id"]).toBe(invoice);
      expect(found.spans_included).toBe(1);
      expect((hit.spans ?? []).map((sp: { span_name: string }) => sp.span_name).sort()).toEqual(["invoice.post", "invoice.receive"]);

      // "Did invoice 123 get through?": with a completion rule on the
      // integration, each result says complete, or waiting or delayed at
      // which stage. The rule reads the integration's stored membership,
      // which the reconciler writes shortly after the integration is
      // created, so this one is polled.
      const rule = await admin.post(`/api/v1/integrations/${integId}/completion-rules`, {
        data: {
          name: "Invoice posted",
          enabled: true,
          start_span_name: "invoice.receive",
          stages: [{ span_names: ["invoice.post"], timeout_seconds: 600 }],
          lookback_seconds: 3600,
        },
      });
      expect(rule.ok(), `creating the completion rule failed: ${rule.status()}`).toBeTruthy();
      let completion: any;
      await expect
        .poll(
          async () => {
            const res = await callTool(request, token, "sluicio_search_traces", {
              integration: `invoices-${stamp}`,
              attributes: [{ key: "Invoice Id", value: invoice }],
              window: "1h",
            });
            completion = res.structuredContent;
            return completion?.results?.[0]?.completion?.[0]?.state;
          },
          { timeout: 90_000 },
        )
        .toBe("complete");
      expect(completion.results[0].completion[0]).toMatchObject({ rule_name: "Invoice posted", stages: 1, reached: 1 });
      // The Messages view reads the same verdict from its own request,
      // after the list has rendered.
      const status = await admin.post(`/api/v1/integrations/${integId}/completion-status`, {
        data: { trace_ids: [traceId], from: completion.results[0].trace_start },
      });
      expect(status.ok(), `completion-status failed: ${status.status()}`).toBeTruthy();
      expect((await status.json()).statuses?.[traceId]?.[0]?.state).toBe("complete");

      const trace = (await callTool(request, token, "sluicio_get_trace", { trace_id: traceId, integration: integId })).structuredContent;
      expect(trace.attribute_labels?.["e2e.iId"]).toBe("Invoice Id");

      const problems: string[] = [];
      validate("sluicio_search_traces", schemaOf("sluicio_search_traces"), found, problems);
      validate("sluicio_search_traces", schemaOf("sluicio_search_traces"), completion, problems);
      validate("sluicio_get_trace", schemaOf("sluicio_get_trace"), trace, problems);
      const integ = (await callTool(request, token, "sluicio_get_integration", { id: integId })).structuredContent;
      expect(integ.integration?.message_columns?.[0]?.label).toBe("Invoice Id");
      validate("sluicio_get_integration", schemaOf("sluicio_get_integration"), integ, problems);
      expect(problems).toEqual([]);
    } finally {
      await admin.delete(`/api/v1/integrations/${integId}`);
    }
  });
});

test.describe("MCP — Streamable HTTP transport", () => {
  test("negotiates a protocol revision it actually speaks", async ({ page, request }) => {
    await logIn(page);
    const token = await mintAdminToken(page.request);

    const current = await rpc(request, token, "initialize", { protocolVersion: "2025-06-18" });
    expect(current.protocolVersion).toBe("2025-06-18");
    expect(current.serverInfo?.name).toBeTruthy();

    // An older client stays supported…
    const legacy = await rpc(request, token, "initialize", { protocolVersion: "2024-11-05" });
    expect(legacy.protocolVersion).toBe("2024-11-05");

    // …and a revision we don't know must not be echoed back, or we claim
    // support for features that don't exist here.
    const future = await rpc(request, token, "initialize", { protocolVersion: "2099-01-01" });
    expect(future.protocolVersion).not.toBe("2099-01-01");
  });

  test("GET and DELETE are refused with an explanation, and an unknown revision is a 400", async ({ page, request }) => {
    await logIn(page);
    const token = await mintAdminToken(page.request);
    const auth = { Authorization: `Bearer ${token}` };

    const get = await request.get("/api/v1/mcp", { headers: auth });
    expect(get.status()).toBe(405);
    expect(get.headers()["allow"]).toContain("POST");
    expect(await get.text()).toContain("event stream");

    const del = await request.delete("/api/v1/mcp", { headers: auth });
    expect(del.status()).toBe(405);

    const bad = await request.post("/api/v1/mcp", {
      headers: { ...auth, "MCP-Protocol-Version": "2099-01-01" },
      data: { jsonrpc: "2.0", id: 1, method: "initialize", params: {} },
    });
    expect(bad.status()).toBe(400);
    expect(await bad.text()).toContain("2025-06-18");
  });

  test("answers SSE to a client that will take nothing else", async ({ page, request }) => {
    await logIn(page);
    const token = await mintAdminToken(page.request);

    const resp = await request.post("/api/v1/mcp", {
      headers: { Authorization: `Bearer ${token}`, Accept: "text/event-stream" },
      data: { jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-06-18" } },
    });
    expect(resp.ok()).toBeTruthy();
    expect(resp.headers()["content-type"]).toContain("text/event-stream");

    const body = await resp.text();
    expect(body.startsWith("event: message\ndata: "), `bad SSE framing: ${body}`).toBeTruthy();
    const payload = JSON.parse(body.slice("event: message\ndata: ".length).trim());
    expect(payload.result.protocolVersion).toBe("2025-06-18");

    // A client listing both gets JSON — one round trip, no framing.
    const json = await request.post("/api/v1/mcp", {
      headers: { Authorization: `Bearer ${token}`, Accept: "application/json, text/event-stream" },
      data: { jsonrpc: "2.0", id: 1, method: "ping" },
    });
    expect(json.headers()["content-type"]).toContain("application/json");
  });

  test("a cross-origin caller cannot ride the browser session", async ({ page }) => {
    // page.request carries the logged-in session cookie, which is
    // exactly what a malicious page would get for free. Adding a foreign
    // Origin must make the call fail — otherwise any site the user has
    // open can invoke tools as them, including the one that writes.
    await logIn(page);
    const forged = await page.request.post("/api/v1/mcp", {
      headers: { Origin: "https://evil.example" },
      data: { jsonrpc: "2.0", id: 1, method: "tools/list" },
    });
    expect(
      forged.status(),
      "a cross-origin request authenticated only by the session cookie must be refused",
    ).toBe(403);

    // Same-origin from the app itself keeps working.
    const ok = await page.request.post("/api/v1/mcp", {
      data: { jsonrpc: "2.0", id: 1, method: "ping" },
    });
    expect(ok.ok()).toBeTruthy();
  });
});
