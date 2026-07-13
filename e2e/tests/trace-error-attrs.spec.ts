// SPDX-License-Identifier: Apache-2.0
//
// API contracts for the two error-visibility features:
//   1. The system setting `map_http_5xx_to_error` round-trips through
//      GET/PATCH /api/v1/cell-settings/system (the ingest-side mapping
//      itself is unit-tested; its effect is verified live with an OTLP
//      emitter).
//   2. Failed-trace alert rules accept attribute predicates (same
//      vocabulary as log rules) and reject bad keys/operators.
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";

let admin: APIRequestContext;

test.beforeAll(async () => {
  admin = await pwRequest.newContext({ baseURL: BASE_URL });
  await admin.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
});

test.afterAll(async () => {
  // Leave the cell as we found it: mapping off.
  await admin.patch("/api/v1/cell-settings/system", { data: { map_http_5xx_to_error: false } });
  await admin.dispose();
});

test("map_http_5xx_to_error round-trips through system settings", async () => {
  // Normalize the starting state — the cell may have the flag on.
  await admin.patch("/api/v1/cell-settings/system", { data: { map_http_5xx_to_error: false } });
  const before = await (await admin.get("/api/v1/cell-settings/system")).json();
  expect(before.map_http_5xx_to_error).toBe(false);

  const patched = await admin.patch("/api/v1/cell-settings/system", {
    data: { map_http_5xx_to_error: true },
  });
  expect(patched.ok()).toBeTruthy();
  expect((await patched.json()).map_http_5xx_to_error).toBe(true);

  const after = await (await admin.get("/api/v1/cell-settings/system")).json();
  expect(after.map_http_5xx_to_error).toBe(true);

  const off = await admin.patch("/api/v1/cell-settings/system", {
    data: { map_http_5xx_to_error: false },
  });
  expect((await off.json()).map_http_5xx_to_error).toBe(false);
});

test("failed-trace rules accept attribute predicates; bad input rejected", async () => {
  const mk = await admin.post("/api/v1/alert-rules", {
    data: {
      name: "e2e failed traces on /checkout",
      severity: "warning",
      enabled: true,
      signal: "trace",
      service_name: "e2e-tea-svc",
      trace_error_spec: {
        threshold: 1,
        window_seconds: 300,
        attrs: [
          { key: "http.route", op: "eq", value: "/checkout" },
          { key: "http.response.status_code", op: "gte", value: "500" },
        ],
      },
      channel_ids: [],
    },
  });
  expect(mk.status()).toBe(201);
  const rule = await mk.json();
  const spec = rule.trace_error_spec ?? rule.rule?.trace_error_spec;
  expect(spec.attrs).toHaveLength(2);
  const ruleID = rule.id ?? rule.rule?.id;
  await admin.delete(`/api/v1/alert-rules/${ruleID}`);

  // Invalid operator → 400, nothing persisted.
  const bad = await admin.post("/api/v1/alert-rules", {
    data: {
      name: "e2e bad op",
      severity: "warning",
      enabled: true,
      signal: "trace",
      service_name: "e2e-tea-svc",
      trace_error_spec: {
        threshold: 1,
        window_seconds: 300,
        attrs: [{ key: "http.route", op: "regexbomb", value: "x" }],
      },
      channel_ids: [],
    },
  });
  expect(bad.status()).toBe(400);

  // Injection-shaped key → 400 (attrKeyRe gate).
  const evil = await admin.post("/api/v1/alert-rules", {
    data: {
      name: "e2e bad key",
      severity: "warning",
      enabled: true,
      signal: "trace",
      service_name: "e2e-tea-svc",
      trace_error_spec: {
        threshold: 1,
        window_seconds: 300,
        attrs: [{ key: "x'] OR 1=1 --", op: "eq", value: "x" }],
      },
      channel_ids: [],
    },
  });
  expect(evil.status()).toBe(400);
});
