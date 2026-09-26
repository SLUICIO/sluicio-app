// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The setup token travels in the URL fragment, and only there.
//
// A fragment is never sent to the server, so the token stays out of
// access logs, out of Referer headers, and out of every proxy on the way.
// Reading the query string instead would undo all of that silently - the
// claim would still work, which is exactly why a test has to say it.

import { describe, expect, it, beforeEach } from "vitest";
import { readClaimToken } from "./Login";

function at(url: string) {
  window.history.replaceState(null, "", url);
}

describe("readClaimToken", () => {
  beforeEach(() => at("/"));

  it("reads the token from the fragment", () => {
    at("/#t=abc123");
    expect(readClaimToken()).toBe("abc123");
  });

  it("removes it from the address bar", () => {
    at("/setup?keep=this#t=abc123");
    readClaimToken();
    expect(window.location.hash).toBe("");
    // The rest of the URL is left alone: only the token goes.
    expect(window.location.pathname).toBe("/setup");
    expect(window.location.search).toBe("?keep=this");
  });

  it("ignores a token in the query string", () => {
    // If this ever passes, the token is reaching the server's logs.
    at("/?t=abc123");
    expect(readClaimToken()).toBe("");
  });

  it("is empty with no fragment at all", () => {
    at("/setup");
    expect(readClaimToken()).toBe("");
  });

  it("tolerates other fragment parameters around it", () => {
    at("/#foo=1&t=abc123&bar=2");
    expect(readClaimToken()).toBe("abc123");
  });

  it("trims whitespace a mail client may have wrapped in", () => {
    at("/#t=%20abc123%20");
    expect(readClaimToken()).toBe("abc123");
  });
});
