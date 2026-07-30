// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Regression guard for a crash that reached a user: clicking
// "+ add a filter" on a self-hosted cell threw
// "crypto.randomUUID is not a function" and took the view down.
//
// `crypto.randomUUID` is secure-context-only. Reached over plain HTTP by
// hostname — http://box.local:8080 — it does not exist. That is the
// ordinary shape of a self-hosted cell before someone puts TLS in front,
// and filters are among the first things anyone clicks.
//
// The e2e suite already clicks that exact button and did not catch it,
// because Playwright drives http://localhost, which IS a secure context.
// No amount of end-to-end coverage on localhost can find this class of
// bug — so the guard has to live here, where the context can be taken
// away on purpose.

import { afterEach, describe, expect, it, vi } from "vitest";
import { uid } from "./uid";

const V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("uid", () => {
  it("returns a v4 uuid in a secure context", () => {
    expect(uid()).toMatch(V4);
  });

  it("still works when randomUUID is missing — the insecure-context case", () => {
    // Exactly what a browser exposes on http://box.local:8080:
    // getRandomValues present, randomUUID absent.
    const real = globalThis.crypto;
    vi.stubGlobal("crypto", {
      getRandomValues: real.getRandomValues.bind(real),
    });
    expect(() => uid()).not.toThrow();
    expect(uid()).toMatch(V4);
  });

  it("survives an environment with no Web Crypto at all", () => {
    vi.stubGlobal("crypto", undefined);
    expect(() => uid()).not.toThrow();
    expect(uid()).toMatch(V4);
  });

  it("does not collide across many calls", () => {
    // These are React keys and filter handles. Duplicates would make two
    // filter rows share an identity, so one would silently overwrite the
    // other — a subtler failure than the crash this file exists for.
    const real = globalThis.crypto;
    vi.stubGlobal("crypto", { getRandomValues: real.getRandomValues.bind(real) });
    const seen = new Set(Array.from({ length: 5000 }, () => uid()));
    expect(seen.size).toBe(5000);
  });
});
