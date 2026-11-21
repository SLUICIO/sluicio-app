// SPDX-License-Identifier: Apache-2.0
//
// Single sign-on on a cell with several organizations.
//
// - The login page never lists organizations: an email address is routed
//   to a provider by its domain alone, and the public provider list is
//   empty once the cell has more than one org.
// - Each email domain belongs to one provider on the cell, assigned by an
//   operator, so one org cannot claim another org's domain.
// - An operator can open any org, and where they are not a member they
//   read it as a viewer and cannot change it.
import { test, expect, request as pwRequest, type APIRequestContext } from "@playwright/test";
import { ADMIN_EMAIL, ADMIN_PASSWORD } from "./fixtures";
import { requireEntitlement } from "./ee-gate";

const BASE_URL = process.env.E2E_BASE_URL || "http://localhost:5173";

test("SSO is routed by email domain, one provider per domain, and an operator visits orgs read-only", async () => {
  const admin: APIRequestContext = await pwRequest.newContext({ baseURL: BASE_URL });
  await admin.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
  await requireEntitlement(admin, "sso");
  const me = await (await admin.get("/api/v1/me")).json();
  test.skip(!me.user?.is_operator, "the e2e admin is not a cell operator here");

  const stamp = Date.now().toString(36);
  const orgIDs: string[] = [];
  try {
    const newOrg = async (slug: string): Promise<string> => {
      const res = await admin.post("/api/v1/operator/orgs", { data: { name: `E2E SSO ${slug}`, slug } });
      expect(res.ok(), `create org ${slug}`).toBeTruthy();
      const id = (await res.json()).id as string;
      orgIDs.push(id);
      return id;
    };
    const slugA = `e2e-sso-a-${stamp}`;
    const slugB = `e2e-sso-b-${stamp}`;
    const orgA = await newOrg(slugA);
    await newOrg(slugB);

    // The operator opens an org they do not belong to, read-only.
    const visit = await (await admin.get("/api/v1/me", { headers: { "X-Sluicio-Org": slugA } })).json();
    expect(visit.principal.org_id).toBe(orgA);
    expect(visit.principal.role).toBe("viewer");
    expect(visit.principal.operator_visit).toBe(true);
    expect((visit.other_orgs ?? []).map((o: { slug: string }) => o.slug)).toEqual(expect.arrayContaining([slugA, slugB]));
    const refused = await admin.post("/api/v1/settings/auth-providers", {
      headers: { "X-Sluicio-Org": slugA },
      data: { name: "IdP A", issuer_url: "https://idp-a.example", client_id: "a" },
    });
    expect(refused.status(), "a visiting operator changed an org's settings").toBe(403);

    // As a member, each org's admin configures its own provider.
    const providerIn = async (slug: string, orgID: string, name: string): Promise<string> => {
      const add = await admin.post(`/api/v1/operator/orgs/${orgID}/members`, { data: { email: ADMIN_EMAIL, role: "admin" } });
      expect(add.ok(), `join ${slug}`).toBeTruthy();
      const res = await admin.post("/api/v1/settings/auth-providers", {
        headers: { "X-Sluicio-Org": slug },
        data: { name, issuer_url: `https://${name}.example`, client_id: name, enabled: true, jit_provisioning: true },
      });
      expect(res.status(), `create provider in ${slug}`).toBe(201);
      return (await res.json()).id as string;
    };
    const provA = await providerIn(slugA, orgA, `idp-a-${stamp}`);
    const provB = await providerIn(slugB, orgIDs[1], `idp-b-${stamp}`);

    const domainA = `a-${stamp}.example`;
    const setA = await admin.put(`/api/v1/operator/auth-providers/${provA}/domains`, { data: { domains: [`@${domainA.toUpperCase()}`] } });
    expect(setA.ok()).toBeTruthy();
    expect((await setA.json()).domains).toEqual([domainA]);
    // B cannot take A's domain.
    const steal = await admin.put(`/api/v1/operator/auth-providers/${provB}/domains`, { data: { domains: [domainA] } });
    expect(steal.status()).toBe(409);

    // Routing, pre-auth: by domain only.
    const anon = await pwRequest.newContext({ baseURL: BASE_URL });
    try {
      const routed = await (await anon.post("/api/v1/auth/sso/discover", { data: { email: `alice@${domainA}` } })).json();
      expect(routed.sso_start).toBe(`/api/v1/auth/sso/${provA}/start`);
      const other = await (await anon.post("/api/v1/auth/sso/discover", { data: { email: `bob@unassigned-${stamp}.example` } })).json();
      expect(other.sso_start).toBeUndefined();
      // With several orgs on the cell, the login page gets no list to show.
      const listed = await (await anon.get("/api/v1/auth/sso/providers")).json();
      expect(listed.providers).toEqual([]);
    } finally {
      await anon.dispose();
    }
  } finally {
    for (const id of orgIDs) await admin.delete(`/api/v1/operator/orgs/${id}`);
    await admin.dispose();
  }
});
