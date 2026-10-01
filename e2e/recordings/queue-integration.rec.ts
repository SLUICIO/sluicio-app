// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Walkthrough: one queue of a message broker as an integration of its
// own. The broker's metrics come in through an OpenTelemetry Collector
// (recordings/rabbitmq), so the integration has series rather than
// messages: build it from the broker's service, narrow it to one queue,
// bind a backlog check to it, and end on a queue whose consumer is gone
// and whose integration says so.
//
// Not a test - a recording. It runs against the recording stack only:
// it creates integrations and checks, and removes the ones it made last
// time.

import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { beat, caption, glideClick, glideTo, installCursor, requireEnv, typeInto } from "./stagecraft";

const BROKER = "rabbitmq";
const QUEUE = "invoices.outbound";
const NAME = "Invoice dispatch";
const SLUG = "invoice-dispatch";
// Prepared before the camera rolls: a queue with no consumer, already red.
const STALLED = { name: "Shipment events", slug: "shipment-events", queue: "shipments.events", threshold: 5000 };

const queueMatchers = (queue: string) => [
  { attribute: "service.name", operator: "equals", value: BROKER, match_group: 0 },
  { attribute: "rabbitmq.queue.name", operator: "equals", value: queue, match_group: 0 },
];

async function signIn(api: APIRequestContext): Promise<void> {
  const res = await api.post("/api/v1/auth/login", {
    data: { email: requireEnv("RECORDING_EMAIL"), password: requireEnv("RECORDING_PASSWORD") },
  });
  expect(res.ok(), `sign-in failed: ${res.status()} - is the recording stack up?`).toBeTruthy();
}

// Removes this recording's integrations, and the checks bound to them
// FIRST: deleting an integration only unbinds its checks, and an unbound
// "ready messages > N" would go on evaluating every queue on the broker.
async function clearPreviousTake(api: APIRequestContext): Promise<void> {
  const ints = await (await api.get("/api/v1/integrations")).json();
  const ours = new Map<string, string>();
  for (const row of ints.integrations ?? []) {
    const it = row.integration ?? row;
    if (it.slug === SLUG || it.slug === STALLED.slug) ours.set(it.id, it.slug);
  }
  if (ours.size === 0) return;
  const rules = await (await api.get("/api/v1/alert-rules")).json();
  for (const r of rules.rules ?? rules ?? []) {
    if (r.integration_id && ours.has(r.integration_id)) {
      const del = await api.delete(`/api/v1/alert-rules/${r.id}`);
      expect(del.ok(), `could not remove check ${r.name}: ${del.status()}`).toBeTruthy();
    }
  }
  for (const id of ours.keys()) {
    const del = await api.delete(`/api/v1/integrations/${id}`);
    expect(del.ok(), `could not remove integration: ${del.status()}`).toBeTruthy();
  }
}

test.beforeAll(async ({ playwright }) => {
  test.setTimeout(6 * 60_000);
  const api = await playwright.request.newContext({ baseURL: requireEnv("RECORDING_BASE_URL") });
  await signIn(api);
  await clearPreviousTake(api);

  // Ready means the broker's queue reports: the preview the services
  // editor uses finds series for it.
  await expect
    .poll(
      async () => {
        const res = await api.post("/api/v1/integrations/preview?range=1h", {
          data: { matchers: queueMatchers(QUEUE), rule_match: "any" },
        });
        return ((await res.json()).metric_series ?? 0) > 0;
      },
      { message: `${QUEUE} is not reporting yet - is the RabbitMQ part of the stack up?`, timeout: 3 * 60_000, intervals: [5_000] },
    )
    .toBe(true);

  // The stalled queue's integration and its check, so the take can end on
  // a red one without waiting for an evaluation on camera.
  const created = await api.post("/api/v1/integrations", {
    data: { slug: STALLED.slug, name: STALLED.name, description: "Shipment events to the carriers.", rule_match: "any", matchers: queueMatchers(STALLED.queue) },
  });
  expect(created.ok(), `create ${STALLED.name}: ${created.status()}`).toBeTruthy();
  const stalledId = ((await created.json()).integration ?? {}).id;
  const rule = await api.post("/api/v1/alert-rules", {
    data: {
      name: "Shipments backlog",
      severity: "warning",
      channel_ids: [],
      signal: "metric",
      integration_id: stalledId,
      spec: {
        metric_name: "rabbitmq.message.current",
        aggregation: "last",
        operator: "gt",
        threshold: STALLED.threshold,
        for_window: "1m",
        attrs: [{ key: "state", op: "eq", value: "ready" }],
      },
    },
  });
  expect(rule.ok(), `create the stalled queue's check: ${rule.status()}`).toBeTruthy();
  await expect
    .poll(async () => (await (await api.get(`/api/v1/integrations/${stalledId}?range=1h`)).json()).status, {
      message: `${STALLED.name} never turned unhealthy - is ${STALLED.queue} above ${STALLED.threshold}?`,
      timeout: 4 * 60_000,
      intervals: [10_000],
    })
    .toBe("unhealthy");
  await api.post("/api/v1/digest/seen");
  await api.dispose();
});

async function signInPage(page: Page): Promise<void> {
  const res = await page.request.post("/api/v1/auth/login", {
    data: { email: requireEnv("RECORDING_EMAIL"), password: requireEnv("RECORDING_PASSWORD") },
  });
  expect(res.ok()).toBeTruthy();
}

test("one queue, one integration", async ({ page }) => {
  await installCursor(page);
  await signInPage(page);

  // 1. Where integrations live.
  await page.goto("/integrations");
  await page.mouse.move(720, 420);
  await caption(page, "One broker, many queues. Each queue can be an integration of its own.");
  await beat(page, 2500);

  // 2. A new one, for the invoices queue.
  await glideClick(page, page.getByRole("link", { name: "New integration" }).first());
  await expect(page).toHaveURL(/\/integrations\/new/);
  await caption(page, "Name it for the flow the queue carries.");
  await typeInto(page, page.getByLabel(/^Name/).first(), NAME);
  await typeInto(page, page.getByLabel("Description"), "Invoices out to the finance partner.");

  // 3. The broker, through its collector: series, not messages.
  await caption(page, "Add the broker. It sends metrics, so you see the series it reports.");
  const addBox = page.getByRole("combobox", { name: "Add a service or pattern" });
  await typeInto(page, addBox, BROKER);
  await beat(page, 500);
  await page.keyboard.press("Enter");
  const members = page.getByRole("list", { name: "Services" });
  await expect(members).toContainText(/series/, { timeout: 20_000 });
  await beat(page, 1800);

  // 4. Narrow it to one queue.
  await caption(page, "Narrow it to one queue.");
  await glideClick(page, page.getByRole("button", { name: `Edit ${BROKER}` }));
  await glideClick(page, page.getByRole("button", { name: "+ condition" }));
  await glideClick(page, page.getByRole("button", { name: "Attribute", exact: true }));
  // The queue name is a resource attribute of the broker's metrics, so it
  // is typed as a key rather than picked from the traces' attribute list.
  await typeInto(page, page.getByPlaceholder("or type a key this cell has not seen yet"), "rabbitmq.queue.name");
  await page.keyboard.press("Escape");
  await glideClick(page, page.getByRole("button", { name: "Attribute value" }));
  await page.keyboard.type(QUEUE, { delay: 70 });
  await beat(page, 300);
  await page.keyboard.press("Enter");
  await glideClick(page, page.getByRole("button", { name: `Close ${BROKER}` }));
  await expect(members).toContainText(`rabbitmq.queue.name is ${QUEUE}`);
  await caption(page, "Only that queue's series are left. A misspelled queue would show zero, before you save.");
  await glideTo(page, page.getByRole("group", { name: "What this integration matches" }));
  await beat(page, 3000);

  // 5. Live at once, from metrics alone.
  await caption(page, "");
  await glideClick(page, page.getByRole("button", { name: /Create integration/ }));
  await expect(page).toHaveURL(/\/integrations\/[0-9a-f-]{36}$/, { timeout: 15_000 });
  await caption(page, "Live at once, from the queue's metrics alone.");
  await beat(page, 1200);
  // The Overview counts messages, and a queue read by a collector has
  // none: the queue's own series are the shot.
  await glideClick(page, page.getByLabel("Integration sections").getByText("Metrics", { exact: true }));
  await expect(page.getByText("rabbitmq.message.current").first()).toBeVisible({ timeout: 15_000 });
  await glideTo(page, page.getByText("rabbitmq.message.current").first());
  await beat(page, 3000);

  // 6. A backlog check, bound to this integration.
  await glideClick(page, page.getByRole("link", { name: /Edit integration/ }).or(page.getByRole("button", { name: /Edit integration/ })).first());
  await glideClick(page, page.getByRole("button", { name: "Alerting", exact: true }).or(page.getByRole("link", { name: "Alerting", exact: true })).first());
  await caption(page, "Bind a backlog check. No queue name needed: on this integration it reads this queue alone.");
  await glideClick(page, page.getByRole("button", { name: "+ Add health check" }));
  await glideClick(page, page.getByRole("menuitem", { name: "Metric / pushed value" }));
  await typeInto(page, page.getByRole("textbox", { name: "Health check name" }), "Invoice backlog");
  await glideClick(page, page.getByText("Choose a metric…").first());
  await page.keyboard.type("rabbitmq.message.current", { delay: 50 });
  await beat(page, 500);
  await glideClick(page, page.getByRole("option", { name: "rabbitmq.message.current", exact: true }).first());
  await glideClick(page, page.getByRole("button", { name: "+ filter" }));
  await page.keyboard.type("state", { delay: 70 });
  await glideClick(page, page.getByRole("option", { name: /^state/ }).first());
  await glideClick(page, page.locator(".attr-pop__item", { hasText: /^ready/ }).first());
  const threshold = page.locator("input.m-rs-num");
  await glideClick(page, threshold);
  await threshold.fill("");
  await threshold.pressSequentially("1000", { delay: 90 });
  await beat(page, 1200);
  await glideClick(page, page.getByRole("button", { name: "Add health check", exact: true }));
  await beat(page, 2000);

  // 7. What a backed-up queue looks like.
  await caption(page, "When a queue backs up, its integration turns red. Shipments has no consumer at all.");
  await page.goto("/integrations");
  await expect(page.getByText(STALLED.name).first()).toBeVisible();
  await glideTo(page, page.getByText(STALLED.name).first());
  await beat(page, 3500);
  await caption(page, "");
  await beat(page, 800);
});
