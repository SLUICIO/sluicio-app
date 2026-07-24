// SPDX-License-Identifier: Apache-2.0
//
// WSO2 API Manager system type / monitoring template: applying the
// built-in "wso2-apim" kind creates its five health checks (failed
// invocations, p95 latency, dead-man volume, error-log spike, JVM heap
// with the heap attribute condition), and remove-template deletes them.
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";
const SVC = "e2e-wso2-apim-svc";

let admin: APIRequestContext;

// NOTE: beforeAll/afterAll run once PER WORKER in Playwright — a cleanup
// there races the other worker's test. The apply/remove lifecycle
// therefore lives entirely inside the one test that needs it.
test.beforeAll(async () => {
  admin = await pwRequest.newContext({ baseURL: BASE_URL });
  await admin.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
});

test.afterAll(async () => {
  await admin.dispose();
});

test("wso2-apim template applies trace, log and metric checks", async () => {
  // Idempotency: drop leftovers from an earlier failed/retried run so
  // apply reports created=5, not skipped=5.
  await admin.post(`/api/v1/services/${SVC}/remove-template`, { data: { kind: "wso2-apim" } });
  const apply = await admin.post(`/api/v1/services/${SVC}/apply-template`, {
    data: { kind: "wso2-apim" },
  });
  expect(apply.ok()).toBeTruthy();
  expect((await apply.json()).created).toBe(5);

  const rules = (await (await admin.get("/api/v1/alert-rules")).json()).rules ?? [];
  const mine = rules.filter((r: { service_name?: string }) => r.service_name === SVC);
  expect(mine).toHaveLength(5);

  const byName = Object.fromEntries(mine.map((r: { name: string }) => [r.name, r]));

  const failed = byName["WSO2 API-M failed API invocations"];
  expect(failed.signal).toBe("trace");
  expect(failed.trace_error_spec.threshold).toBe(1);

  const latency = byName["WSO2 API-M response time"];
  expect(latency.trace_latency_spec.threshold_ms).toBe(2000);
  expect(latency.trace_latency_spec.aggregation).toBe("p95");

  const silent = byName["WSO2 API-M gateway silent"];
  expect(silent.trace_volume_spec.threshold).toBe(1);
  expect(silent.trace_volume_spec.window_seconds).toBe(900);

  const logs = byName["WSO2 API-M error logs spiking"];
  expect(logs.signal).toBe("log");
  expect(logs.log_spec.min_severity).toBe(17);
  expect(logs.log_spec.threshold).toBe(10);

  const heap = byName["WSO2 API-M JVM heap high"];
  expect(heap.signal).toBe("metric");
  expect(heap.spec.metric_name).toBe("jvm.memory.used");
  expect(heap.spec.attrs).toEqual([{ key: "jvm.memory.type", op: "eq", value: "heap" }]);

  // Removing the template deletes exactly those checks.
  const rm = await admin.post(`/api/v1/services/${SVC}/remove-template`, { data: { kind: "wso2-apim" } });
  expect((await rm.json()).removed).toBe(5);
});

test("wso2-apim appears in the system-types catalog", async () => {
  const cat = await (await admin.get("/api/v1/system-types")).json();
  const types = cat.system_types ?? cat.types ?? cat;
  const wso2 = (Array.isArray(types) ? types : []).find(
    (t: { key: string }) => t.key === "wso2-apim",
  );
  expect(wso2).toBeTruthy();
  expect(wso2.label).toBe("WSO2 API Manager");
  expect(wso2.is_system ?? wso2.system).toBeTruthy();
});
