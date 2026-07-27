// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The agent write path (issue #8, WS2): an agent files a proposal, a
// human approves it, the rule changes.
//
// The assertions that matter are the ones about what does NOT happen.
// A proposal must not change anything until approved, `before` must be
// snapshotted by the server rather than taken from the caller, and a
// target edited after filing must block approval instead of silently
// reverting the human's edit. Each of those, if broken, fails quietly:
// the API answers 200 and the audit trail records a change that never
// happened.

import { test, expect, type APIRequestContext } from "@playwright/test";
import { logIn } from "./fixtures";

// Rules created here are deleted afterwards. Every rule left behind is
// work the alert engine repeats on each tick forever, so a spec that
// litters makes every LATER spec slower — and the delivery-timing tests
// are the ones that notice first.
const createdRules: { api: APIRequestContext; id: string }[] = [];

test.afterAll(async () => {
  for (const { api, id } of createdRules) {
    await api.delete(`/api/v1/alert-rules/${id}`).catch(() => {});
  }
  createdRules.length = 0;
});

/** Creates a metric rule to propose against; returns its id. */
async function createRule(api: APIRequestContext, name: string): Promise<string> {
  const res = await api.post("/api/v1/alert-rules", {
    data: {
      name,
      signal: "metric",
      severity: "warning",
      enabled: true,
      spec: {
        metric_name: "queue.depth",
        aggregation: "avg",
        operator: "gt",
        threshold: 5,
        for_window: "5m",
      },
    },
  });
  expect(res.ok(), `create rule: ${res.status()}`).toBeTruthy();
  const id = (await res.json()).id;
  createdRules.push({ api, id });
  return id;
}

async function getRule(api: APIRequestContext, id: string) {
  const res = await api.get("/api/v1/alert-rules");
  const rules = (await res.json()).rules ?? [];
  return rules.find((r: { id: string }) => r.id === id);
}

test.describe("Proposals — the agent write path", () => {
  test("a proposal changes nothing until approved, then applies through the normal path", async ({ page }) => {
    await logIn(page);
    const api = page.request;
    const ruleId = await createRule(api, `e2e-prop-${Date.now()}`);

    const created = await api.post("/api/v1/proposals", {
      data: {
        target_kind: "alert_rule",
        target_id: ruleId,
        rationale: "Fired 40 times in 24h and every instance auto-resolved within 2 minutes.",
        // `before` is deliberately NOT sent, and a wrong one is sent for
        // severity to prove the server ignores caller-supplied values.
        changes: [
          { field: "threshold", after: 8 },
          { field: "severity", before: "critical", after: "critical" },
        ],
      },
    });
    expect(created.status(), await created.text()).toBe(201);
    const proposal = await created.json();

    // The server snapshots `before` itself. If it echoed the caller's
    // value, the drift guard could be defeated by simply lying.
    const thresholdChange = proposal.changes.find((c: { field: string }) => c.field === "threshold");
    const severityChange = proposal.changes.find((c: { field: string }) => c.field === "severity");
    expect(thresholdChange.before).toBe(5);
    expect(severityChange.before).toBe("warning"); // NOT the "critical" we sent

    // Filing must not have touched the rule.
    let rule = await getRule(api, ruleId);
    expect(rule.spec.threshold).toBe(5);
    expect(rule.severity).toBe("warning");

    // It shows up in the pending queue with a count for the nav badge.
    const listed = await (await api.get("/api/v1/proposals?state=pending")).json();
    expect(listed.pending_count).toBeGreaterThan(0);
    expect(listed.proposals.some((p: { id: string }) => p.id === proposal.id)).toBeTruthy();

    // Approving applies it through the ordinary rule update.
    const approved = await api.post(`/api/v1/proposals/${proposal.id}/approve`, { data: {} });
    expect(approved.status(), await approved.text()).toBe(200);
    expect((await approved.json()).state).toBe("approved");

    rule = await getRule(api, ruleId);
    expect(rule.spec.threshold).toBe(8);
    expect(rule.severity).toBe("critical");

    // Deciding twice is a conflict, not a second apply.
    const again = await api.post(`/api/v1/proposals/${proposal.id}/approve`, { data: {} });
    expect(again.status()).toBe(409);
  });

  test("a target edited after filing blocks approval until explicitly forced", async ({ page }) => {
    await logIn(page);
    const api = page.request;
    const ruleId = await createRule(api, `e2e-drift-${Date.now()}`);

    const proposal = await (
      await api.post("/api/v1/proposals", {
        data: {
          target_kind: "alert_rule",
          target_id: ruleId,
          rationale: "Threshold is too low for this queue's normal depth.",
          changes: [{ field: "threshold", after: 12 }],
        },
      })
    ).json();

    // A human edits the same field while the proposal sits in the queue.
    const rule = await getRule(api, ruleId);
    rule.spec.threshold = 6;
    const edited = await api.put(`/api/v1/alert-rules/${ruleId}`, { data: rule });
    expect(edited.ok()).toBeTruthy();

    // The reviewer sees the drift BEFORE deciding, not in an error.
    const detail = await (await api.get(`/api/v1/proposals/${proposal.id}`)).json();
    expect(detail.drifted_fields).toContain("threshold");

    // Approving is refused, and crucially the human's 6 survives.
    const refused = await api.post(`/api/v1/proposals/${proposal.id}/approve`, { data: {} });
    expect(refused.status()).toBe(409);
    expect((await getRule(api, ruleId)).spec.threshold).toBe(6);

    // force is the explicit override.
    const forced = await api.post(`/api/v1/proposals/${proposal.id}/approve`, {
      data: { force: true, note: "reviewed, the agent's reading still holds" },
    });
    expect(forced.status(), await forced.text()).toBe(200);
    expect((await getRule(api, ruleId)).spec.threshold).toBe(12);
  });

  test("filing supersedes an earlier pending proposal for the same target", async ({ page }) => {
    await logIn(page);
    const api = page.request;
    const ruleId = await createRule(api, `e2e-supersede-${Date.now()}`);

    const body = (after: number) => ({
      data: {
        target_kind: "alert_rule",
        target_id: ruleId,
        rationale: `proposing ${after}`,
        changes: [{ field: "threshold", after }],
      },
    });
    const first = await (await api.post("/api/v1/proposals", body(8))).json();
    const second = await (await api.post("/api/v1/proposals", body(9))).json();

    // Only the newest survives: competing edits to one target make a
    // queue nobody can reason about, and only the latest `before` is
    // still current.
    expect((await (await api.get(`/api/v1/proposals/${first.id}`)).json()).proposal.state).toBe("superseded");
    expect((await (await api.get(`/api/v1/proposals/${second.id}`)).json()).proposal.state).toBe("pending");
  });

  test("rejects proposals that are unreviewable or out of scope", async ({ page }) => {
    await logIn(page);
    const api = page.request;
    const ruleId = await createRule(api, `e2e-invalid-${Date.now()}`);

    // No rationale: a diff with no reason isn't reviewable.
    const noReason = await api.post("/api/v1/proposals", {
      data: { target_kind: "alert_rule", target_id: ruleId, rationale: "  ", changes: [{ field: "threshold", after: 8 }] },
    });
    expect(noReason.status()).toBe(400);

    // Retargeting is authoring, not tuning — an agent able to repoint a
    // check could disable monitoring while appearing to tune it.
    const retarget = await api.post("/api/v1/proposals", {
      data: {
        target_kind: "alert_rule",
        target_id: ruleId,
        rationale: "point this somewhere else",
        changes: [{ field: "service_name", after: "somewhere-else" }],
      },
    });
    expect(retarget.status()).toBe(400);

    // Unknown target kinds are refused rather than stored unapplyable.
    const unknownKind = await api.post("/api/v1/proposals", {
      data: { target_kind: "not_a_thing", target_id: ruleId, rationale: "x", changes: [{ field: "threshold", after: 1 }] },
    });
    expect(unknownKind.status()).toBe(400);
  });
});
