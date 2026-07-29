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
import { logIn } from "./fixtures";

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

    const args: Record<string, Record<string, unknown>> = {
      sluicio_get_integration: { id: integrations[0].id },
      sluicio_get_system: { id: systems[0].id },
      sluicio_get_trace: { trace_id: traces[0].trace_id },
      sluicio_metric_series: { metric: metrics?.[0]?.name ?? "queue.depth", window: "7d" },
      sluicio_search_traces: { window: "7d", errors_only: false, limit: 5 },
      sluicio_search_logs: { window: "7d", limit: 5 },
    };

    try {
      const problems: string[] = [];
      let checked = 0;
      for (const tool of tools) {
        expect(tool.outputSchema, `${tool.name} declares no outputSchema`).toBeTruthy();
        expect(tool.annotations, `${tool.name} has no annotations`).toBeTruthy();
        // The writer is exercised separately, below — calling it here
        // would leave a proposal in the queue on every run.
        if (tool.annotations.readOnlyHint !== true) continue;

        const result = await callTool(request, token, tool.name, args[tool.name] ?? { window: "24h" });
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
      expect(checked, "no read-only tool was actually called").toBeGreaterThan(10);
      expect(problems, `declared output schemas no longer describe what cell-api returns:\n${problems.join("\n")}`)
        .toEqual([]);
    } finally {
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
