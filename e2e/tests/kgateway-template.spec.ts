// SPDX-License-Identifier: Apache-2.0
//
// kgateway system types: the gateway proxy ("kgateway") and the control
// plane ("kgateway-controller") are two types because they are two
// processes with disjoint telemetry. Applying each creates its checks
// with the scopes, splits and filters the types declare, and
// remove-template deletes exactly those. The proxy type detects on
// nothing (it is plain Envoy), the control plane on kgateway_.
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { logIn, ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";

let admin: APIRequestContext;

type Rule = { name: string; service_name?: string; [k: string]: any };

// NOTE: beforeAll/afterAll run once PER WORKER in Playwright, so a
// cleanup there races the other worker's test. Each apply/remove
// lifecycle lives inside its own test, on its own service.
test.beforeAll(async () => {
  admin = await pwRequest.newContext({ baseURL: BASE_URL });
  await admin.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
});

test.afterAll(async () => {
  await admin.dispose();
});

async function applyFresh(svc: string, kind: string, want: number): Promise<Record<string, Rule>> {
  // Drop leftovers from an earlier failed run so apply reports created,
  // not skipped.
  await admin.post(`/api/v1/services/${svc}/remove-template`, { data: { kind } });
  const apply = await admin.post(`/api/v1/services/${svc}/apply-template`, { data: { kind } });
  expect(apply.ok()).toBeTruthy();
  expect((await apply.json()).created).toBe(want);
  const rules: Rule[] = (await (await admin.get("/api/v1/alert-rules")).json()).rules ?? [];
  const mine = rules.filter((r) => r.service_name === svc);
  expect(mine).toHaveLength(want);
  return Object.fromEntries(mine.map((r) => [r.name, r]));
}

test("kgateway applies the gateway proxy's checks", async () => {
  const svc = "e2e-kgateway-proxy-svc";
  const byName = await applyFresh(svc, "kgateway", 9);

  const failing = byName["Requests failing"];
  expect(failing.signal).toBe("trace");
  expect(failing.trace_error_spec.threshold).toBe(1);
  expect(failing.check_scope).toBe("flow");

  // NC never reaches an Envoy cluster, so only the span can say it.
  const missing = byName["Route points at a missing backend"];
  expect(missing.trace_error_spec.attrs).toEqual([
    { key: "response_flags", op: "matches", value: "(^|,)NC(,|$)" },
  ]);

  const noHealthy = byName["No healthy backend"];
  expect(noHealthy.spec.metric_name).toBe("envoy_cluster_upstream_cx_none_healthy");
  expect(noHealthy.spec.aggregation).toBe("increase");
  expect(noHealthy.spec.split_by).toBe("envoy_cluster_name");
  // The proxy's own control-plane cluster is reported by its own check.
  expect(noHealthy.spec.attrs).toEqual([{ key: "envoy_cluster_name", op: "neq", value: "xds_cluster" }]);

  expect(byName["Backend refusing connections"].spec.metric_name).toBe("envoy_cluster_upstream_cx_connect_fail");
  expect(byName["Backend timeouts"].spec.metric_name).toBe("envoy_cluster_upstream_rq_timeout");
  expect(byName["Slow requests"].trace_latency_spec.threshold_ms).toBe(2000);
  expect(byName["Gateway silent"].trace_volume_spec.window_seconds).toBe(900);

  const lost = byName["Proxy lost the control plane"];
  expect(lost.spec.metric_name).toBe("envoy_control_plane_connected_state");
  expect(lost.check_scope).toBe("process");

  const down = byName["Gateway proxy not running"];
  expect(down.spec.metric_name).toBe("envoy_server_live");
  expect(down.spec.fire_on_no_data).toBe(true);

  const rm = await admin.post(`/api/v1/services/${svc}/remove-template`, { data: { kind: "kgateway" } });
  expect((await rm.json()).removed).toBe(9);
});

test("kgateway-controller applies the control plane's checks", async () => {
  const svc = "e2e-kgateway-controller-svc";
  const byName = await applyFresh(svc, "kgateway-controller", 5);

  const rejected = byName["Gateway rejected its configuration"];
  expect(rejected.signal).toBe("metric");
  expect(rejected.spec.metric_name).toBe("kgateway_envoy_xds_rejects_active");
  expect(rejected.spec.aggregation).toBe("max");
  expect(rejected.spec.split_by).toBe("gateway_name");
  expect(rejected.severity).toBe("critical");

  const down = byName["Control plane not running"];
  expect(down.spec.metric_name).toBe("kgateway_build_info");
  expect(down.spec.fire_on_no_data).toBe(true);

  expect(byName["Reconcile errors"].spec.attrs).toEqual([{ key: "result", op: "eq", value: "error" }]);
  expect(byName["Translation failing"].spec.attrs).toEqual([{ key: "result", op: "neq", value: "success" }]);
  expect(byName["Status writes failing"].spec.split_by).toBe("kind");

  const rm = await admin.post(`/api/v1/services/${svc}/remove-template`, {
    data: { kind: "kgateway-controller" },
  });
  expect((await rm.json()).removed).toBe(5);
});

test("both kgateway types are in the catalog, with their detection and docs", async () => {
  const cat = await (await admin.get("/api/v1/system-types")).json();
  const types: Array<Record<string, any>> = cat.system_types ?? cat.types ?? cat;
  const byKey = Object.fromEntries((Array.isArray(types) ? types : []).map((t) => [t.key, t]));

  const proxy = byKey["kgateway"];
  expect(proxy.label).toBe("kgateway");
  expect(proxy.is_system).toBeTruthy();
  expect(proxy.detect_prefixes).toEqual([]);
  expect(proxy.docs_url).toBe("https://docs.sluicio.com/system-types/kgateway/");

  const ctrl = byKey["kgateway-controller"];
  expect(ctrl.label).toBe("kgateway control plane");
  expect(ctrl.detect_prefixes).toEqual(["kgateway_"]);
  expect(ctrl.docs_url).toBe("https://docs.sluicio.com/system-types/kgateway-controller/");
});

test("System types page says the kgateway proxy is picked by hand", async ({ page }) => {
  await logIn(page);
  await page.goto("/system-types");
  // The label and the key are both "kgateway"; either toggles the row.
  await page.getByText("kgateway", { exact: true }).first().click();
  await expect(page.getByText(/won't auto-detect/)).toBeVisible();
  await expect(page.getByText("Route points at a missing backend", { exact: true })).toBeVisible();
  await expect(page.getByText(/undefined/)).toHaveCount(0);
});
