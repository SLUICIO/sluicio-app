// SPDX-License-Identifier: Apache-2.0
//
// Cloning an integration, and the RBAC line it must not cross.
//
// A clone is a create whose content comes from an existing row, so the
// danger is that it becomes a sideways route to something the caller
// could not have made directly. Granting a group access to an
// integration requires ADMIN; creating one only requires editor. So the
// property under test is not "the copy is faithful" but "the copy is
// never more than the caller was entitled to".
import { test, expect } from "@playwright/test";
import { logIn } from "./fixtures";

const stamp = Date.now().toString(36);

test.describe("Integration clone", () => {
  test("copies the settings that describe the integration", async ({ page }) => {
    await logIn(page);
    const src = await page.request.post("/api/v1/integrations", {
      data: {
        slug: `clone-src-${stamp}`,
        name: `Clone Source ${stamp}`,
        description: "the original",
        matchers: [{ operator: "equals", value: "payment-service" }],
      },
    });
    expect(src.ok(), `create source: ${src.status()}`).toBeTruthy();
    const srcId = (await src.json()).integration.id as string;
    const made: string[] = [srcId];

    try {
      // A health check bound to the source — it is a setting of the
      // integration, so the clone must carry it.
      const rule = await page.request.post("/api/v1/alert-rules", {
        data: {
          name: `clone-check-${stamp}`,
          signal: "metric",
          integration_id: srcId,
          severity: "warning",
          spec: {
            metric_name: "queue.depth", aggregation: "last",
            operator: "gt", threshold: 99999, for_window: "5m",
          },
        },
      });
      expect(rule.ok(), `create check: ${rule.status()}`).toBeTruthy();

      const res = await page.request.post(`/api/v1/integrations/${srcId}/clone`, {
        data: { name: `Clone Copy ${stamp}`, slug: `clone-copy-${stamp}` },
      });
      expect(res.status(), await res.text()).toBe(201);
      const body = await res.json();
      const newId = body.integration.id as string;
      made.push(newId);

      expect(newId).not.toBe(srcId);
      expect(body.integration.name).toBe(`Clone Copy ${stamp}`);
      expect(body.integration.description).toBe("the original");

      const clone = await (await page.request.get(`/api/v1/integrations/${newId}`)).json();
      expect(clone.matchers.map((m: { value: string }) => m.value)).toEqual(["payment-service"]);
      // Never inherited: it serves an unauthenticated endpoint.
      expect(clone.integration.badge_public ?? false).toBe(false);

      const rules = await (
        await page.request.get(`/api/v1/alert-rules?integration_id=${newId}`)
      ).json();
      expect(
        (rules.rules ?? []).map((r: { name: string }) => r.name),
        "the clone did not carry the source's health check",
      ).toContain(`clone-check-${stamp}`);
    } finally {
      for (const iid of made) await page.request.delete(`/api/v1/integrations/${iid}`);
    }
  });

  test("refuses a slug that is already taken", async ({ page }) => {
    await logIn(page);
    const src = await page.request.post("/api/v1/integrations", {
      data: { slug: `clone-dup-${stamp}`, name: `Clone Dup ${stamp}`, matchers: [] },
    });
    const srcId = (await src.json()).integration.id as string;
    try {
      // Cloning onto its own slug: a 400 naming the problem, not a 500.
      const res = await page.request.post(`/api/v1/integrations/${srcId}/clone`, {
        data: { name: "whatever", slug: `clone-dup-${stamp}` },
      });
      expect(res.status()).toBe(400);
      expect((await res.text()).toLowerCase()).toContain("slug");
    } finally {
      await page.request.delete(`/api/v1/integrations/${srcId}`);
    }
  });

  test("an admin's clone reports that it carried team access", async ({ page }) => {
    // The seeded admin is org-admin, so the response must say the grants
    // came along. The editor half of this rule is covered by the unit
    // tests on PlanClone, which do not need a second identity.
    await logIn(page);
    const src = await page.request.post("/api/v1/integrations", {
      data: { slug: `clone-acl-${stamp}`, name: `Clone ACL ${stamp}`, matchers: [] },
    });
    const srcId = (await src.json()).integration.id as string;
    const made = [srcId];
    try {
      const res = await page.request.post(`/api/v1/integrations/${srcId}/clone`, {
        data: { name: `Clone ACL copy ${stamp}`, slug: `clone-acl-copy-${stamp}` },
      });
      expect(res.status()).toBe(201);
      const body = await res.json();
      made.push(body.integration.id);
      expect(body.copied_group_access).toBe(true);
    } finally {
      for (const iid of made) await page.request.delete(`/api/v1/integrations/${iid}`);
    }
  });
});
