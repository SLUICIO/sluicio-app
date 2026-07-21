// SPDX-License-Identifier: Apache-2.0
//
// KrakenD system type / monitoring template: applying the built-in
// "krakend" kind creates its five health checks (failed 5xx traces with
// attribute conditions, p95 latency, dead-man volume, plus the two
// transport-failure metric counters), and remove-template deletes them.
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { logIn, ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

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
  expect((await apply.json()).created).toBe(5);

  const rules = (await (await admin.get("/api/v1/alert-rules")).json()).rules ?? [];
  const mine = rules.filter((r: { service_name?: string }) => r.service_name === SVC);
  expect(mine).toHaveLength(5);

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

  const unreachable = byName["KrakenD backend unreachable"];
  expect(unreachable.signal).toBe("metric");
  expect(unreachable.spec.metric_name).toBe("http.client.request.failed.count");
  expect(unreachable.spec.aggregation).toBe("increase");

  const timeouts = byName["KrakenD backend timeouts"];
  expect(timeouts.spec.metric_name).toBe("http.client.request.timedout.count");

  // Removing the template deletes exactly those checks.
  const rm = await admin.post(`/api/v1/services/${SVC}/remove-template`, { data: { kind: "krakend" } });
  expect((await rm.json()).removed).toBe(5);
});

test("System types page renders krakend's trace checks readably", async ({ page }) => {
  await logIn(page);
  await page.goto("/system-types");
  // The WHOLE row header toggles the detail (2026-07-21) — click the
  // label itself, not the ▸ caret.
  const row = page.locator("div").filter({ hasText: "KrakenD API Gateway" }).last();
  await row.getByText("KrakenD API Gateway", { exact: true }).click();
  // Every check renders a real summary — the trace signals once fell
  // through to the metric formatter as "metric · undefined undefined …".
  await expect(page.getByText(/≥1 failed traces in window \[http\.response\.status_code gte 500\]/)).toBeVisible();
  await expect(page.getByText(/p95 latency ≥ 2000 ms/)).toBeVisible();
  await expect(page.getByText(/fewer than 1 traces in window/)).toBeVisible();
  await expect(page.getByText(/undefined/)).toHaveCount(0);
  // The action buttons must NOT toggle the row: after hovering/clicking
  // Export (an <a download>, no navigation) the checks stay visible.
  await row.getByRole("link", { name: "Export" }).click();
  await expect(page.getByText(/p95 latency ≥ 2000 ms/)).toBeVisible();
  // Built-ins carry a key-derived docs link (docs-first convention).
  await expect(row.getByRole("link", { name: /Docs/ })).toHaveAttribute(
    "href",
    "https://docs.sluicio.com/system-types/krakend/",
  );
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
