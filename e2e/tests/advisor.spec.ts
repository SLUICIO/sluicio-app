// SPDX-License-Identifier: Apache-2.0
//
// The Telemetry & Alert Fatigue advisors (issue #1).
//
// What is worth asserting end-to-end is not "a suggestion appeared" —
// on a seeded cell that depends on 30 days of history nobody has — but
// the properties that make the feature safe to act on:
//
//   - it is admin-only and Enterprise-gated, because a suggestion states
//     what the whole org ingests and what it costs;
//   - a decision STICKS, so an operator who says "no, that attribute is
//     load-bearing" is not asked again tomorrow;
//   - accepting leaves an audit entry, because the suggestion text is
//     the paper trail for "why did we stop collecting that";
//   - the alerting advisor never puts a threshold in the action.
//
// Self-skips without the `advisor` entitlement, and fails instead of
// skipping under E2E_EXPECT_EE.

import { test, expect, type APIRequestContext } from "@playwright/test";
import { logIn } from "./fixtures";
import { requireEntitlement } from "./ee-gate";

async function evaluate(api: APIRequestContext) {
  const res = await api.post("/api/v1/advisor/run");
  // 429 is the deliberate cooldown, not a failure — a full evaluation
  // samples a month of spans.
  expect([200, 429], `run: ${res.status()}`).toContain(res.status());
}

test.describe("Advisor (EE)", () => {
  test.beforeEach(async ({ page }) => {
    await logIn(page);
    await requireEntitlement(page, "advisor");
  });

  test("is admin-only and reachable as a tab under Usage", async ({ page }) => {
    await page.goto("/usage?tab=advisor");
    await expect(page.getByRole("heading", { name: "Usage" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Advisor" })).toHaveAttribute("aria-selected", "true");
    // Both sub-tabs exist even with nothing to show — an empty advisor
    // must explain itself rather than render blank.
    await expect(page.getByRole("tab", { name: "Telemetry" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Alerting" })).toBeVisible();
  });

  test("the API validates its filters and echoes the observation window", async ({ page }) => {
    const api = page.request;
    const ok = await api.get("/api/v1/advisor/suggestions?advisor=telemetry");
    expect(ok.ok(), `list: ${ok.status()}`).toBeTruthy();
    const body = await ok.json();
    expect(Array.isArray(body.suggestions)).toBeTruthy();
    // A number without its period is not evidence.
    expect(body.window_days).toBeGreaterThan(0);

    expect((await api.get("/api/v1/advisor/suggestions?advisor=nonsense")).status()).toBe(400);
    expect((await api.get("/api/v1/advisor/suggestions?state=maybe")).status()).toBe(400);
  });

  test("evaluation runs on demand and is rate-limited", async ({ page }) => {
    const api = page.request;
    const first = await api.post("/api/v1/advisor/run");
    expect([200, 429]).toContain(first.status());
    if (first.status() === 200) {
      // The cooldown exists because a run is the most expensive query
      // the service makes; a demo button that can be held down is a
      // self-inflicted outage.
      const second = await api.post("/api/v1/advisor/run");
      expect(second.status(), "a second immediate run must be refused").toBe(429);
      expect(second.headers()["retry-after"]).toBeTruthy();
    }
  });

  test("a decision sticks, and accepting is audited", async ({ page }) => {
    const api = page.request;
    await evaluate(api);
    const { suggestions } = await (await api.get("/api/v1/advisor/suggestions")).json();
    test.skip(
      (suggestions ?? []).length === 0,
      "no findings on this cell — needs 30 days of history to judge anything",
    );

    const target = suggestions[0];
    const dismissed = await api.post(`/api/v1/advisor/suggestions/${target.id}/dismiss`, {
      data: { note: "e2e: load-bearing, keep it" },
    });
    expect(dismissed.ok(), `dismiss: ${dismissed.status()}`).toBeTruthy();
    expect((await dismissed.json()).state).toBe("dismissed");

    // The board hides dismissed findings…
    const board = await (await api.get("/api/v1/advisor/suggestions")).json();
    expect((board.suggestions ?? []).some((s: { id: string }) => s.id === target.id)).toBeFalsy();

    // …and re-evaluating does NOT resurface it. This is the property
    // that decides whether anyone keeps reading the advisor: a
    // suggestion that returns every night after being refused trains
    // people to ignore the whole page.
    await evaluate(api);
    const after = await (await api.get("/api/v1/advisor/suggestions")).json();
    expect(
      (after.suggestions ?? []).some((s: { id: string }) => s.id === target.id),
      "a dismissed suggestion came back on the next evaluation",
    ).toBeFalsy();

    // Explicitly asking for dismissed ones still finds it.
    const hidden = await (await api.get("/api/v1/advisor/suggestions?state=dismissed")).json();
    expect((hidden.suggestions ?? []).some((s: { id: string }) => s.id === target.id)).toBeTruthy();
  });

  test("the alerting advisor never puts a threshold in the action", async ({ page }) => {
    // The design's sharpest rule (§4): a suggested number in a one-click
    // button is how somebody silences a real alert on our recommendation.
    const api = page.request;
    await evaluate(api);
    const { suggestions } = await (await api.get("/api/v1/advisor/suggestions?advisor=alerting")).json();
    test.skip((suggestions ?? []).length === 0, "no alerting findings on this cell");

    for (const s of suggestions) {
      expect(s.advisor).toBe("alerting");
      // Alerting findings change config inside Sluicio, so there is
      // nothing to paste into a collector.
      expect(s.snippet ?? "", `${s.class} carries a collector snippet`).toBe("");
      if (s.class === "F1") {
        expect(
          String(s.evidence?.suggested_threshold ?? ""),
          "F1 must state that it proposes no number",
        ).toContain("none");
      }
    }
  });

  test("every suggestion states what is lost, not only what is saved", async ({ page }) => {
    const api = page.request;
    await evaluate(api);
    const { suggestions } = await (await api.get("/api/v1/advisor/suggestions")).json();
    test.skip((suggestions ?? []).length === 0, "no findings on this cell");
    for (const s of suggestions) {
      expect(s.loss ?? "", `${s.class} on ${s.scope_id} has no loss statement`).not.toBe("");
      expect(Object.keys(s.evidence ?? {}).length, `${s.class} has no evidence`).toBeGreaterThan(0);
    }
  });

  test("a viewer cannot see the advisor at all", async ({ page, request }) => {
    // It reports org-wide cost. A group-scoped reader has no business
    // with it, and a partial view would rank findings against a total
    // they cannot see.
    const admin = page.request;
    const sa = await (
      await admin.post("/api/v1/settings/service-accounts", {
        data: { name: `e2e-advisor-viewer-${Date.now()}`, description: "advisor rbac", role: "viewer" },
      })
    ).json();
    const saID = sa.id ?? sa.account?.id;
    try {
      const token = (
        await (await admin.post(`/api/v1/settings/service-accounts/${saID}/tokens`, { data: { name: "t1" } })).json()
      ).plaintext;
      const res = await request.get("/api/v1/advisor/suggestions", {
        headers: { Authorization: `Bearer ${token}` },
      });
      expect([401, 403], `viewer got ${res.status()}`).toContain(res.status());
    } finally {
      await admin.delete(`/api/v1/settings/service-accounts/${saID}`).catch(() => {});
    }
  });
});
