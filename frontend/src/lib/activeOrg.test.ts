// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// After a single sign-on the callback names the org whose identity
// provider was used. A user in several orgs lands there, and the
// parameter leaves the address bar before anything reads the URL.

import { beforeEach, describe, expect, it, vi } from "vitest";
import { adoptOrgFromURL, getActiveOrgSlug, setActiveOrgSlug } from "./activeOrg";

// The test runtime has no working localStorage; a Map stands in for it.
function memoryStorage(): Storage {
  const m = new Map<string, string>();
  return {
    get length() {
      return m.size;
    },
    clear: () => m.clear(),
    getItem: (k) => m.get(k) ?? null,
    key: (i) => [...m.keys()][i] ?? null,
    removeItem: (k) => void m.delete(k),
    setItem: (k, v) => void m.set(k, String(v)),
  };
}

describe("adoptOrgFromURL", () => {
  beforeEach(() => {
    vi.stubGlobal("localStorage", memoryStorage());
    setActiveOrgSlug("partner");
    window.history.replaceState(null, "", "/");
  });

  it("pins the org the sign-in named and drops the parameter", () => {
    window.history.replaceState(null, "", "/integrations?env=prod&org=customer-a#top");
    adoptOrgFromURL();
    expect(getActiveOrgSlug()).toBe("customer-a");
    expect(window.location.pathname + window.location.search + window.location.hash).toBe("/integrations?env=prod#top");
  });

  it("leaves the pin alone without one", () => {
    window.history.replaceState(null, "", "/integrations?env=prod");
    adoptOrgFromURL();
    expect(getActiveOrgSlug()).toBe("partner");
    expect(window.location.search).toBe("?env=prod");
  });
});
