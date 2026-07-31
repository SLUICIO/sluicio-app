// SPDX-License-Identifier: Apache-2.0
//
// One service, several integrations: every number on the integration
// page must describe the INTEGRATION, not the service carrying it.
//
// This is the shape a Node-RED runtime produces — one service emitting
// every flow, with an attribute (node_red.flow.id) naming which. An
// integration is then a SLICE of that service, and any panel that reports
// the service's totals instead is quietly answering a different question.
//
// The reported symptom: an integration with 2 clean traces showed
// "100% of failures come from <service>" because its member rows carried
// the service's 3 failures from other flows, while the header, the flow
// graph and the message list all (correctly) showed none. Every surface
// disagreeing with its neighbour is worse than any single wrong number —
// the user cannot tell which one to believe.
//
// So the assertions are deliberately about AGREEMENT across surfaces,
// not just about one endpoint returning the value we like.
import { test, expect, type APIRequestContext } from "@playwright/test";
import { logIn } from "./fixtures";
import { encodeTraceExport } from "./otlp";

const INGEST_URL = process.env.E2E_INGEST_URL || "http://localhost:4318";

const FLOW_KEY = "node_red.flow.id";
const MINE = "tab_mine";
const OTHER = "tab_other";
// The integration's own traffic: clean. The rest of the service: failing.
// The contrast is the whole fixture — equal counts would let a
// service-wide number masquerade as a scoped one.
const MINE_OK = 2;
const OTHER_ERR = 3;

async function ingest(
  admin: APIRequestContext,
  key: string,
  service: string,
  flow: string,
  count: number,
  error: boolean,
): Promise<void> {
  const body = encodeTraceExport(
    service,
    Array.from({ length: count }, (_, i) => ({
      name: `${flow}-span-${i}`,
      error,
      attrs: { [FLOW_KEY]: flow },
    })),
  );
  const res = await admin.post(`${INGEST_URL}/v1/traces`, {
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/x-protobuf" },
    data: body,
    failOnStatusCode: false,
  });
  expect(res.ok(), `ingest ${flow} failed: ${res.status()}`).toBeTruthy();
}

test("an attribute-scoped integration reports its own slice, not its service's totals", async ({
  page,
}) => {
  test.setTimeout(180_000);
  await logIn(page);
  const admin = page.request;
  const stamp = Date.now().toString(36);
  const service = `e2e-flowsvc-${stamp}`;

  // Minting must succeed, and a failure must SAY SO. This was a
  // test.skip() first, and the whole test then vanished from the full
  // suite while passing in isolation — the summary line read green with
  // one fewer test in it, which is precisely the silent-skip failure
  // this suite exists to avoid. If the cell cannot mint a key, that is
  // news, not a reason to stop testing.
  const keyRes = await admin.post("/api/v1/ingest-keys", {
    data: { name: `e2e-scope-${stamp}` },
    failOnStatusCode: false,
  });
  expect(
    keyRes.ok(),
    `could not mint an ingest key (${keyRes.status()}): ${await keyRes.text()}`,
  ).toBeTruthy();
  const key = (await keyRes.json()).key as string;

  await ingest(admin, key, service, MINE, MINE_OK, false);
  await ingest(admin, key, service, OTHER, OTHER_ERR, true);

  // service.name selects MEMBERSHIP; the second matcher is a row-level
  // predicate AND-ed onto the integration's telemetry queries.
  const mk = await admin.post("/api/v1/integrations", {
    data: {
      slug: `flow-scope-${stamp}`,
      name: `Flow Scope ${stamp}`,
      matchers: [
        { attribute: "service.name", operator: "equals", value: service },
        { attribute: FLOW_KEY, operator: "equals", value: MINE },
      ],
    },
  });
  expect(mk.ok(), `creating the integration failed: ${mk.status()}`).toBeTruthy();
  const integrationId = (await mk.json()).integration.id as string;

  try {
    // Membership is reconciled asynchronously; wait for the service to
    // be claimed before asserting anything about per-member counts.
    await expect
      .poll(
        async () => {
          const d = await (await admin.get(`/api/v1/integrations/${integrationId}?range=24h`)).json();
          return (d.services ?? []).length;
        },
        { timeout: 90_000 },
      )
      .toBeGreaterThan(0);

    const detail = await (
      await admin.get(`/api/v1/integrations/${integrationId}?range=24h`)
    ).json();

    // 1. The member row — the surface that was wrong. It must describe
    //    the slice, not the service.
    const member = (detail.services ?? []).find(
      (s: { service_name: string }) => s.service_name === service,
    );
    expect(member, "the service is not listed as a member").toBeTruthy();
    expect(
      member.error_trace_count,
      "the member row is reporting the SERVICE's failures; those belong to another flow",
    ).toBe(0);
    expect(member.trace_count).toBe(MINE_OK);

    // 2. The flow-graph node, which already scoped correctly. Asserting
    //    it here is what makes this a test about agreement: if a future
    //    change fixes one projection and not the other, the page goes
    //    back to contradicting itself and this fails.
    const node = (detail.flow?.nodes ?? detail.nodes ?? []).find(
      (n: { service_name: string }) => n.service_name === service,
    );
    if (node) {
      expect(node.trace_count, "flow node disagrees with the member row").toBe(member.trace_count);
      expect(node.error_trace_count, "flow node disagrees with the member row").toBe(
        member.error_trace_count,
      );
    }

    // 3. The overview list, which the user reads before clicking in.
    const list = await (await admin.get("/api/v1/integrations?range=24h")).json();
    // IntegrationSummary embeds integrations.Integration, so the id is
    // inline on the row rather than nested under `integration`.
    const summary = (list.integrations ?? []).find(
      (i: { id: string }) => i.id === integrationId,
    );
    expect(summary, "the integration is missing from the list").toBeTruthy();
    expect(summary.trace_count, "list total disagrees with the detail page").toBe(MINE_OK);
    expect(summary.error_trace_count, "the list is counting another flow's failures").toBe(0);

    // 4. The message list, reached by clicking "errors" — the surface
    //    that always told the truth and so exposed the contradiction.
    const errs = await (
      await admin.post("/api/v1/messages/search", {
        data: {
          range: "24h",
          limit: 50,
          filters: [
            { field: "integration", op: "is", value: `Flow Scope ${stamp}` },
            { field: "status", op: "is", value: "err only" },
          ],
        },
      })
    ).json();
    expect(
      errs.total,
      "the message list found failures the integration's own counters say do not exist",
    ).toBe(0);

    // 5. The service itself must still report everything. Scoping the
    //    integration must not hide that the service is failing — that
    //    would trade a confusing page for a dangerous one.
    const svc = await (
      await admin.get(`/api/v1/services/${encodeURIComponent(service)}?range=24h`)
    ).json();
    expect(
      svc.stats.error_trace_count,
      "the service is under-reporting its own failures",
    ).toBe(OTHER_ERR);
    expect(svc.stats.trace_count).toBe(MINE_OK + OTHER_ERR);
  } finally {
    await admin.delete(`/api/v1/integrations/${integrationId}`);
  }
});
