// SPDX-License-Identifier: Apache-2.0
//
// The Enterprise entitlement gate for the e2e suite.
//
// Every EE spec needs a licensed cell, and against an unlicensed one the
// only sane behaviour is to skip — the features genuinely aren't there.
// But "skip" is indistinguishable from "pass" in a summary line, and that
// is exactly how ~26 EE tests (the whole audit log, EE RBAC, MFA policy)
// came to run in NO environment at all: unlicensed locally, and CI never
// supplied a key either. A green suite said nothing about Enterprise.
//
// So the gate has two modes. Without E2E_EXPECT_EE it skips, which keeps
// the suite runnable against any cell. With E2E_EXPECT_EE=1 — set by the
// CI job that injects a licence — a missing entitlement is a FAILURE:
// if the key expired, was revoked, or the secret went missing, the run
// goes red instead of quietly reverting to a pile of skips.
//
// Set E2E_EXPECT_EE=1 anywhere a licence is supposed to be present.

import { test, type APIRequestContext, type Page } from "@playwright/test";

/** All five entitlements the licence can carry. */
export type Entitlement =
  | "sso"
  | "rbac_advanced"
  | "audit_log"
  | "retention_long"
  | "mfa_policy";

/** True when the caller asserts a licensed cell (CI with the secret set). */
export function expectsEE(): boolean {
  const v = (process.env.E2E_EXPECT_EE ?? "").toLowerCase();
  return v === "1" || v === "true" || v === "yes";
}

type Requestish = Page | { request: APIRequestContext } | APIRequestContext;

function asRequest(src: Requestish): APIRequestContext {
  // Page and fixtures expose .request; an APIRequestContext is already one.
  return "request" in src ? (src as { request: APIRequestContext }).request : (src as APIRequestContext);
}

/** Reads /api/v1/license once. Returns null when the call fails. */
async function licenseState(src: Requestish): Promise<Record<string, unknown> | null> {
  try {
    const res = await asRequest(src).get("/api/v1/license");
    if (!res.ok()) return null;
    return (await res.json()) as Record<string, unknown>;
  } catch {
    return null;
  }
}

/** True when the cell reports `feature` as entitled. */
export async function entitled(src: Requestish, feature: Entitlement): Promise<boolean> {
  const st = await licenseState(src);
  const features = st?.features as Record<string, boolean> | undefined;
  return Boolean(features?.[feature]);
}

/** True when the cell reports any licence at all. */
export async function licensed(src: Requestish): Promise<boolean> {
  const st = await licenseState(src);
  return Boolean(st?.licensed);
}

/**
 * Gate a test on `feature`. Call from a test body or a beforeEach.
 *
 * Entitled  → returns, the test runs.
 * Not entitled, E2E_EXPECT_EE unset → skips (unlicensed cell, expected).
 * Not entitled, E2E_EXPECT_EE set   → throws, failing the test loudly.
 */
export async function requireEntitlement(src: Requestish, feature: Entitlement): Promise<void> {
  if (await entitled(src, feature)) return;
  if (expectsEE()) {
    throw new Error(
      `E2E_EXPECT_EE is set but the cell reports no "${feature}" entitlement. ` +
        `The licence is missing, expired or malformed — check SLUICIO_LICENSE_KEY ` +
        `on the cell (GET /api/v1/license shows what it parsed).`,
    );
  }
  test.skip(true, `cell has no ${feature} entitlement`);
}

/** As requireEntitlement, but gates on holding any licence at all. */
export async function requireLicense(src: Requestish): Promise<void> {
  if (await licensed(src)) return;
  if (expectsEE()) {
    throw new Error(
      "E2E_EXPECT_EE is set but the cell reports no enterprise licence. " +
        "Check SLUICIO_LICENSE_KEY on the cell (GET /api/v1/license).",
    );
  }
  test.skip(true, "cell has no enterprise license — EE suite skipped");
}
