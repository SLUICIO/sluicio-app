// SPDX-License-Identifier: Apache-2.0
//
// An integration is the union of its rules, and that is the wrong answer
// for a flow that is defined by going through all of them.
//
// Reported from a demo cell: three rules, one per service, two of them
// carrying an attribute condition. A trace that satisfied the b2b-gateway
// rule was pulled in whatever the order-validator span said - the gateway
// rule had been satisfied on its own, and a union asks no more than that.
//
// rule_match = "all" asks the question of the TRACE: every rule must be
// satisfied by some span of the same trace. The fixture is the smallest
// thing that can tell the two apart, which is two rules and a trace that
// satisfies only one of them.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";
import { encodeTraceExport } from "./otlp";
import crypto from "node:crypto";

const INGEST_URL = process.env.E2E_INGEST_URL || "http://localhost:4318";

test("rules combined with all are judged per trace, not per span", async ({ page }) => {
  test.setTimeout(180_000);
  await logIn(page);
  const admin = page.request;
  const stamp = Date.now().toString(36);
  const svcA = `e2e-rm-a-${stamp}`;
  const svcB = `e2e-rm-b-${stamp}`;

  const keyRes = await admin.post("/api/v1/ingest-keys", {
    data: { name: `e2e-rulematch-${stamp}` },
    failOnStatusCode: false,
  });
  expect(keyRes.ok(), `could not mint an ingest key (${keyRes.status()})`).toBeTruthy();
  const key = (await keyRes.json()).key as string;

  const both = crypto.randomBytes(16).toString("hex"); // a=1 and b=3
  const onlyB = crypto.randomBytes(16).toString("hex"); // a=2 but b=3
  const send = async (service: string, spans: Parameters<typeof encodeTraceExport>[1]) => {
    const res = await admin.post(`${INGEST_URL}/v1/traces`, {
      headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/x-protobuf" },
      data: encodeTraceExport(service, spans),
      failOnStatusCode: false,
    });
    expect(res.ok(), `ingest into ${service} failed: ${res.status()}`).toBeTruthy();
  };
  await send(svcA, [
    { name: "recv", attrs: { a: "1" }, traceId: both },
    { name: "recv", attrs: { a: "2" }, traceId: onlyB },
  ]);
  await send(svcB, [
    { name: "send", attrs: { b: "3" }, traceId: both },
    { name: "send", attrs: { b: "3" }, traceId: onlyB },
  ]);

  const mk = await admin.post("/api/v1/integrations", {
    data: {
      slug: `rule-match-${stamp}`,
      name: `Rule Match ${stamp}`,
      rule_match: "all",
      matchers: [
        { attribute: "service.name", operator: "equals", value: svcA, match_group: 0 },
        { attribute: "a", operator: "equals", value: "1", match_group: 0 },
        { attribute: "service.name", operator: "equals", value: svcB, match_group: 1 },
        { attribute: "b", operator: "equals", value: "3", match_group: 1 },
      ],
    },
  });
  expect(mk.ok(), `creating the integration failed: ${mk.status()}`).toBeTruthy();
  const body = await mk.json();
  const id = body.integration.id as string;
  expect(body.integration.rule_match, "the mode did not survive the create").toBe("all");

  const messages = async (name: string) => {
    const res = await admin.post("/api/v1/messages/search", {
      data: { range: "1h", filters: [{ field: "integration", op: "is", value: name }] },
    });
    expect(res.ok(), `messages search failed: ${res.status()}`).toBeTruthy();
    return ((await res.json()).results ?? []) as { trace_id: string }[];
  };
  const name = body.integration.name as string;

  try {
    // Ingest is asynchronous; wait for the qualifying trace to appear.
    await expect
      .poll(async () => (await messages(name)).map((r) => r.trace_id), { timeout: 90_000 })
      .toContain(both);

    const withAll = (await messages(name)).map((r) => r.trace_id);
    expect(
      withAll,
      "a trace satisfying only one rule is in a slice that asks for both",
    ).not.toContain(onlyB);

    // The union is still the union: switching back returns the trace the
    // reporter did not want, which is what makes this a choice rather
    // than a fix applied to everybody.
    const upd = await admin.put(`/api/v1/integrations/${id}`, {
      data: { name, description: "", rule_match: "any" },
    });
    expect(upd.ok(), `switching the mode failed: ${upd.status()}`).toBeTruthy();
    await expect
      .poll(async () => (await messages(name)).map((r) => r.trace_id), { timeout: 30_000 })
      .toContain(onlyB);
  } finally {
    await admin.delete(`/api/v1/integrations/${id}`);
  }
});
