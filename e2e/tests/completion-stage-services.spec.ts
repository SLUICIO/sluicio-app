// SPDX-License-Identifier: Apache-2.0
//
// The stage service picker in a trace completion rule offered only the
// members that had sent in the last 24 hours, so an integration of three
// services where two run once a day showed one. It now offers the
// integration's members, like the service flow, whatever their traffic
// in the window.
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";
import { encodeTraceExport } from "./otlp";

const INGEST_URL = process.env.E2E_INGEST_URL || "http://localhost:4318";

test("the stage service picker offers every member, not only the recent ones", async ({ page }) => {
  test.setTimeout(180_000);
  await logIn(page);
  const admin = page.request;
  const stamp = Date.now().toString(36);
  const recent = `e2e-css-recent-${stamp}`;
  const daily = `e2e-css-daily-${stamp}`;

  const keyRes = await admin.post("/api/v1/ingest-keys", { data: { name: `e2e-css-${stamp}` }, failOnStatusCode: false });
  expect(keyRes.ok(), `could not mint an ingest key (${keyRes.status()})`).toBeTruthy();
  const key = (await keyRes.json()).key as string;
  const send = async (service: string, atMs: number) => {
    const res = await admin.post(`${INGEST_URL}/v1/traces`, {
      headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/x-protobuf" },
      data: encodeTraceExport(service, [{ name: "create", atMs }]),
      failOnStatusCode: false,
    });
    expect(res.ok(), `ingest into ${service} failed: ${res.status()}`).toBeTruthy();
  };
  await send(recent, Date.now());
  await send(daily, Date.now() - 30 * 3600 * 1000);

  const mk = await admin.post("/api/v1/integrations", {
    data: {
      slug: `stage-services-${stamp}`,
      name: `Stage Services ${stamp}`,
      matchers: [
        { attribute: "service.name", operator: "equals", value: recent, match_group: 0 },
        { attribute: "service.name", operator: "equals", value: daily, match_group: 1 },
      ],
    },
  });
  expect(mk.ok(), `creating the integration failed: ${mk.status()}`).toBeTruthy();
  const id = (await mk.json()).integration.id as string;

  try {
    // Membership follows ingest and the reconciler; wait for both.
    await expect
      .poll(
        async () => {
          const res = await admin.get(`/api/v1/integrations/${id}/span-names`);
          return res.ok() ? (((await res.json()).services ?? []) as string[]) : [];
        },
        { timeout: 120_000, message: "a member with no traffic in 24 hours is missing" },
      )
      .toEqual([daily, recent].sort());
  } finally {
    await admin.delete(`/api/v1/integrations/${id}`);
  }
});
