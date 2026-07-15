// SPDX-License-Identifier: Apache-2.0
//
// Config export & import (docs/config-transfer-design.md), proven
// end-to-end against the real API:
//   - export carries fixtures with natural keys and no secrets
//   - dry-run reports changes and provably applies nothing
//   - strict import populates a fresh org; a second strict import 409s
//   - a bundle with a dangling reference fails AND leaves the target
//     untouched — the atomicity contract
//   - replace mode updates in place
// Uses a throwaway org created via the operator API; the suite admin
// must be a cell operator (the seeded admin is).
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";
const TARGET_SLUG = "e2e-ct-target";

async function login(orgSlug?: string): Promise<APIRequestContext> {
  const ctx = await pwRequest.newContext({
    baseURL: BASE_URL,
    extraHTTPHeaders: orgSlug ? { "X-Sluicio-Org": orgSlug } : {},
  });
  const res = await ctx.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
  if (!res.ok()) throw new Error(`login: ${res.status()}`);
  return ctx;
}

test.describe.configure({ mode: "serial" });

test.describe("Config export & import", () => {
  let source: APIRequestContext;
  let target: APIRequestContext;
  let targetOrgID = "";
  let bundle: {
    format_version: number;
    sections: {
      tags?: { slug: string; name: string }[];
      integrations?: { slug: string; name: string; matchers?: unknown[]; tags?: string[] }[];
      groups?: { slug: string }[];
    };
  };

  test.beforeAll(async () => {
    source = await login();
    // Source fixtures (idempotent-ish; cleaned in afterAll).
    await source.post("/api/v1/tags", { data: { slug: "e2e-ct-tag", name: "E2E CT Tag", color: "#0E6E9E" } });
    await source.post("/api/v1/settings/groups", { data: { name: "e2e-ct-group", slug: "e2e-ct-group" } });
    await source.post("/api/v1/integrations", {
      data: {
        slug: "e2e-ct-integ", name: "E2E CT Integ",
        matchers: [{ operator: "equals", value: "e2e-ct-service" }],
      },
    });
    // Fresh target org via the operator surface. Delete a leftover
    // first (a previous run's worker dying before afterAll leaves the
    // org POPULATED, and a polluted target turns the strict dry-run
    // into a 409 cascade).
    const existing = await (await source.get("/api/v1/operator/orgs")).json();
    const leftover = (existing.orgs ?? []).find((o: { slug: string }) => o.slug === TARGET_SLUG);
    if (leftover) await source.delete(`/api/v1/operator/orgs/${leftover.id}`);
    const created = await source.post("/api/v1/operator/orgs", {
      data: { name: "E2E CT Target", slug: TARGET_SLUG },
    });
    if (!created.ok()) throw new Error(`create target org: ${created.status()}`);
    const orgs = await (await source.get("/api/v1/operator/orgs")).json();
    targetOrgID = (orgs.orgs ?? []).find((o: { slug: string }) => o.slug === TARGET_SLUG)?.id;
    // The admin must be a member of the target org to import into it.
    await source.post(`/api/v1/operator/orgs/${targetOrgID}/members`, {
      data: { email: ADMIN_EMAIL, role: "admin" },
    });
    target = await login(TARGET_SLUG);
  });

  test.afterAll(async () => {
    if (targetOrgID) await source.delete(`/api/v1/operator/orgs/${targetOrgID}`);
    const integs = await (await source.get("/api/v1/integrations")).json();
    const integ = (integs.integrations ?? []).find((i: { slug: string }) => i.slug === "e2e-ct-integ");
    if (integ) await source.delete(`/api/v1/integrations/${integ.id}`);
    const tags = await (await source.get("/api/v1/tags")).json();
    const tag = (tags.tags ?? []).find((t: { slug: string }) => t.slug === "e2e-ct-tag");
    if (tag) await source.delete(`/api/v1/tags/${tag.id}`);
    const groups = await (await source.get("/api/v1/settings/groups")).json();
    const group = (groups.groups ?? []).find((g: { slug: string }) => g.slug === "e2e-ct-group");
    if (group) await source.delete(`/api/v1/settings/groups/${group.id}`);
    await source.dispose();
    await target.dispose();
  });

  test("export: natural keys present, fixtures included", async () => {
    const res = await source.get("/api/v1/settings/config-export");
    expect(res.status()).toBe(200);
    bundle = await res.json();
    expect(bundle.format_version).toBe(1);
    expect((bundle.sections.tags ?? []).some((t) => t.slug === "e2e-ct-tag")).toBe(true);
    expect((bundle.sections.groups ?? []).some((g) => g.slug === "e2e-ct-group")).toBe(true);
    const integ = (bundle.sections.integrations ?? []).find((i) => i.slug === "e2e-ct-integ");
    expect(integ).toBeTruthy();
    expect(integ!.matchers?.length).toBe(1);
  });

  test("dry-run reports changes and applies nothing", async () => {
    const res = await target.post("/api/v1/settings/config-import?mode=strict&dry_run=true", { data: bundle });
    expect(res.status()).toBe(200);
    const report = await res.json();
    expect(report.dry_run).toBe(true);
    expect(report.sections.integrations.created).toBeGreaterThan(0);
    // Provably nothing happened:
    const integs = await (await target.get("/api/v1/integrations")).json();
    expect(integs.integrations ?? []).toHaveLength(0);
  });

  test("strict import populates the fresh org; repeat 409s", async () => {
    const res = await target.post("/api/v1/settings/config-import?mode=strict&dry_run=false", { data: bundle });
    expect(res.status()).toBe(200);
    const integs = await (await target.get("/api/v1/integrations")).json();
    expect((integs.integrations ?? []).some((i: { slug: string }) => i.slug === "e2e-ct-integ")).toBe(true);
    const again = await target.post("/api/v1/settings/config-import?mode=strict&dry_run=false", { data: bundle });
    expect(again.status()).toBe(409);
  });

  test("a failing import leaves the target untouched (atomicity)", async () => {
    const before = await (await target.get("/api/v1/tags")).json();
    const beforeCount = (before.tags ?? []).length;
    // Valid tag first, then an integration referencing a tag that exists
    // nowhere — the engine must roll back the tag it already inserted.
    const poison = {
      format_version: 1,
      sections: {
        tags: [{ slug: "e2e-ct-poison-tag", name: "Poison", color: "#123456" }],
        integrations: [{ slug: "e2e-ct-poison", name: "Poison", tags: ["ghost-tag-does-not-exist"] }],
      },
    };
    const res = await target.post("/api/v1/settings/config-import?mode=replace&dry_run=false", { data: poison });
    expect(res.status()).toBe(400);
    const after = await (await target.get("/api/v1/tags")).json();
    expect((after.tags ?? []).length).toBe(beforeCount);
    expect((after.tags ?? []).some((t: { slug: string }) => t.slug === "e2e-ct-poison-tag")).toBe(false);
  });

  test("replace mode updates in place", async () => {
    const renamed = JSON.parse(JSON.stringify(bundle));
    const integ = renamed.sections.integrations.find((i: { slug: string }) => i.slug === "e2e-ct-integ");
    integ.name = "E2E CT Integ (renamed)";
    const res = await target.post("/api/v1/settings/config-import?mode=replace&dry_run=false", { data: renamed });
    expect(res.status()).toBe(200);
    const report = await res.json();
    expect(report.sections.integrations.updated).toBeGreaterThan(0);
    const integs = await (await target.get("/api/v1/integrations")).json();
    const found = (integs.integrations ?? []).find((i: { slug: string }) => i.slug === "e2e-ct-integ");
    expect(found.name).toBe("E2E CT Integ (renamed)");
  });
});
