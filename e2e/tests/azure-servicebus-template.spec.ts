// SPDX-License-Identifier: Apache-2.0
//
// Azure Service Bus system type: the built-in "azure-servicebus" kind
// (grounded in the Integrio azureservicebusreceiver's documented
// servicebus.* gauges) applies four metric checks, each split by
// queue/subscription so firings name the backed-up entity.
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";
const SVC = "e2e-asb-svc";

let admin: APIRequestContext;

test.beforeAll(async () => {
  admin = await pwRequest.newContext({ baseURL: BASE_URL });
  await admin.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
});

test.afterAll(async () => {
  await admin.dispose();
});

test("azure-servicebus template applies split-by metric checks", async () => {
  await admin.post(`/api/v1/services/${SVC}/remove-template`, { data: { kind: "azure-servicebus" } });
  const apply = await admin.post(`/api/v1/services/${SVC}/apply-template`, {
    data: { kind: "azure-servicebus" },
  });
  expect(apply.ok()).toBeTruthy();
  expect((await apply.json()).created).toBe(4);

  const rules = (await (await admin.get("/api/v1/alert-rules")).json()).rules ?? [];
  const mine = rules.filter((r: { service_name?: string }) => r.service_name === SVC);
  expect(mine).toHaveLength(4);
  const byName = Object.fromEntries(mine.map((r: { name: string }) => [r.name, r]));

  const dlq = byName["Service Bus dead-lettered messages"];
  expect(dlq.signal).toBe("metric");
  expect(dlq.spec.metric_name).toBe("servicebus.queue.deadletter_messages");
  expect(dlq.spec.split_by).toBe("queue");
  expect(dlq.spec.threshold).toBe(0);

  const subBacklog = byName["Service Bus subscription backlog"];
  expect(subBacklog.spec.metric_name).toBe("servicebus.topic.subscription.active_messages");
  expect(subBacklog.spec.split_by).toBe("subscription");
  expect(subBacklog.spec.threshold).toBe(5000);

  const rm = await admin.post(`/api/v1/services/${SVC}/remove-template`, { data: { kind: "azure-servicebus" } });
  expect((await rm.json()).removed).toBe(4);
});

test("azure-servicebus appears in the catalog with detection prefix", async () => {
  const cat = await (await admin.get("/api/v1/system-types")).json();
  const types = cat.system_types ?? cat.types ?? cat;
  const asb = (Array.isArray(types) ? types : []).find(
    (t: { key: string }) => t.key === "azure-servicebus",
  );
  expect(asb).toBeTruthy();
  expect(asb.label).toBe("Azure Service Bus");
  expect(asb.is_system ?? asb.system).toBeTruthy();
  expect(asb.detect_prefixes).toContain("servicebus.");
});
