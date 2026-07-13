// SPDX-License-Identifier: Apache-2.0
//
// KrakenD system type / monitoring template: applying the built-in
// "krakend" kind creates its three trace-signal health checks (failed
// 5xx traces with attribute conditions, p95 latency, dead-man volume),
// and remove-template deletes them again.
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";
const SVC = "e2e-krakend-svc";

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

test("krakend template applies three trace-signal checks", async () => {
  // Idempotency: drop leftovers from an earlier failed/retried run so
  // apply reports created=3, not skipped=3.
  await admin.post(`/api/v1/services/${SVC}/remove-template`, { data: { kind: "krakend" } });
  const apply = await admin.post(`/api/v1/services/${SVC}/apply-template`, {
    data: { kind: "krakend" },
  });
  expect(apply.ok()).toBeTruthy();
  expect((await apply.json()).created).toBe(3);

  const rules = (await (await admin.get("/api/v1/alert-rules")).json()).rules ?? [];
  const mine = rules.filter((r: { service_name?: string }) => r.service_name === SVC);
  expect(mine).toHaveLength(3);

  const byName = Object.fromEntries(mine.map((r: { name: string }) => [r.name, r]));

  const fiveXX = byName["KrakenD 5xx responses"];
  expect(fiveXX.signal).toBe("trace");
  expect(fiveXX.trace_error_spec.threshold).toBe(1);
  expect(fiveXX.trace_error_spec.attrs).toEqual([
    { key: "http.response.status_code", op: "gte", value: "500" },
  ]);

  const latency = byName["KrakenD response time"];
  expect(latency.trace_latency_spec.threshold_ms).toBe(2000);
  expect(latency.trace_latency_spec.aggregation).toBe("p95");

  const silent = byName["KrakenD gateway silent"];
  expect(silent.trace_volume_spec.threshold).toBe(1);
  expect(silent.trace_volume_spec.window_seconds).toBe(900);

  // Removing the template deletes exactly those checks.
  const rm = await admin.post(`/api/v1/services/${SVC}/remove-template`, { data: { kind: "krakend" } });
  expect((await rm.json()).removed).toBe(3);
});

test("krakend appears in the system-types catalog", async () => {
  const cat = await (await admin.get("/api/v1/system-types")).json();
  const types = cat.system_types ?? cat.types ?? cat;
  const krakend = (Array.isArray(types) ? types : []).find(
    (t: { key: string }) => t.key === "krakend",
  );
  expect(krakend).toBeTruthy();
  expect(krakend.label).toBe("KrakenD API Gateway");
  expect(krakend.is_system ?? krakend.system).toBeTruthy();
});
