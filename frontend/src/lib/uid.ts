// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Local identifiers for UI objects — filter rows, draft views, list keys.
//
// This exists because `crypto.randomUUID()` is a SECURE-CONTEXT-ONLY
// API. Only https:// and localhost qualify; a self-hosted cell reached
// over plain HTTP by hostname or IP — http://box.local:8080,
// http://10.0.0.5 — is not a secure context, and `crypto.randomUUID`
// is simply undefined there. Calling it throws
// "crypto.randomUUID is not a function" and takes the component down.
//
// That is not an exotic deployment. Sluicio's images terminate plain
// HTTP and leave TLS to the operator's reverse proxy (docs/security.md),
// so every self-hosted cell looks exactly like this until someone puts
// a certificate in front of it — and "add a filter" is one of the first
// things a new operator clicks.
//
// It is also invisible to our own tests: Playwright drives
// http://localhost, which IS a secure context, so the e2e suite happily
// clicks the button that crashes for a real user on a LAN hostname.
//
// `crypto.getRandomValues` has no such restriction, so the fallback is a
// genuine RFC-4122 v4 UUID rather than a weaker shape.

/** Generates a v4 UUID, working in insecure contexts. */
export function uid(): string {
  const c = globalThis.crypto as Crypto | undefined;
  if (typeof c?.randomUUID === "function") {
    return c.randomUUID();
  }
  if (typeof c?.getRandomValues === "function") {
    const b = new Uint8Array(16);
    c.getRandomValues(b);
    b[6] = (b[6] & 0x0f) | 0x40; // version 4
    b[8] = (b[8] & 0x3f) | 0x80; // variant 10xx
    const hex = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  }
  // Last resort for an environment with no Web Crypto at all. Weaker,
  // and acceptable only because these ids are React keys and local
  // filter handles — they are never persisted as security tokens, never
  // sent as credentials, and never have to be unguessable. Losing a
  // filter row to a collision is the worst outcome available here.
  const rnd = () => Math.floor(Math.random() * 0x10000).toString(16).padStart(4, "0");
  return `${rnd()}${rnd()}-${rnd()}-4${rnd().slice(1)}-a${rnd().slice(1)}-${rnd()}${rnd()}${rnd()}`;
}
