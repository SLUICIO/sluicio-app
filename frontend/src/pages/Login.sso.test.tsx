// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Email-first sign-in. The login page never lists the organizations on a
// cell: it asks the server where an email address signs in, decided by
// the address's domain alone, and steps the password field aside when
// that is single sign-on.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";

const calls = vi.hoisted(() => ({ discover: [] as string[], login: 0 }));
vi.mock("../api/client", () => ({
  api: {
    listSsoProviders: () => Promise.resolve({ providers: [] }),
    installState: () => Promise.resolve({}),
    discoverSso: (email: string) => {
      calls.discover.push(email);
      return Promise.resolve(email.endsWith("@customer-a.example") ? { sso_start: "/api/v1/auth/sso/p1/start" } : {});
    },
    login: () => {
      calls.login++;
      return Promise.resolve({});
    },
  },
}));
vi.mock("../lib/useBranding", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/useBranding")>()),
  useBranding: () => null,
  useLoginBranding: () => null,
  brandingLogoFor: () => null,
  applyBrandingToDocument: () => {},
}));

const { default: Login, emailDomain } = await import("./Login");

describe("emailDomain", () => {
  it("is only read from a complete address", () => {
    expect(emailDomain("alice@Customer-A.example")).toBe("customer-a.example");
    expect(emailDomain("alice@customer")).toBe("");
    expect(emailDomain("alice@")).toBe("");
    expect(emailDomain("alice")).toBe("");
  });
});

describe("email-first sign-in", () => {
  beforeEach(() => {
    calls.discover = [];
    calls.login = 0;
    vi.stubGlobal("fetch", () => Promise.resolve(new Response(JSON.stringify({ announcements: [] }))));
  });

  const typeEmail = (v: string) => fireEvent.change(screen.getByLabelText("Email"), { target: { value: v } });

  it("sends an address in an SSO domain to its provider instead of asking for a password", async () => {
    render(<Login onSuccess={async () => {}} />);
    expect(screen.getByLabelText("Password")).toBeTruthy();

    typeEmail("alice@customer-a.example");
    const go = await screen.findByRole("button", { name: "Continue with single sign-on" });
    expect(screen.queryByLabelText("Password")).toBeNull();

    const assign = vi.fn();
    vi.stubGlobal("location", { ...window.location, pathname: "/login", search: "", assign });
    fireEvent.click(go);
    expect(assign).toHaveBeenCalledWith("/api/v1/auth/sso/p1/start?return=%2F");
    expect(calls.login).toBe(0);
    vi.unstubAllGlobals();
  });

  it("keeps the password for any other address, and a way back to it inside an SSO domain", async () => {
    render(<Login onSuccess={async () => {}} />);
    typeEmail("bob@elsewhere.example");
    await waitFor(() => expect(calls.discover).toContain("bob@elsewhere.example"));
    expect(screen.getByLabelText("Password")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Sign in" })).toBeTruthy();

    typeEmail("op@customer-a.example");
    fireEvent.click(await screen.findByRole("button", { name: "Sign in with a password instead" }));
    expect(screen.getByLabelText("Password")).toBeTruthy();
  });

  it("asks nothing until the address is complete", async () => {
    render(<Login onSuccess={async () => {}} />);
    typeEmail("alice@cust");
    await new Promise((r) => setTimeout(r, 400));
    expect(calls.discover).toEqual([]);
  });
});
