// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The property that matters: two checks on the same metric with
// DIFFERENT filters must not be born with the same name. That collision
// is what produced three rows called "httpcheck.status alert" which no
// list could tell apart.

import { describe, expect, it } from "vitest";
import { defaultRuleName } from "./ruleName";
import type { RuleAttrFilter } from "../api/types";

const eq = (key: string, value: string): RuleAttrFilter => ({ key, op: "eq", value });

describe("defaultRuleName", () => {
  it("keeps the short name when there is nothing to disambiguate", () => {
    expect(defaultRuleName("httpcheck.status")).toBe("httpcheck.status alert");
    expect(defaultRuleName("httpcheck.status", [])).toBe("httpcheck.status alert");
  });

  it("names the filter that scopes the check", () => {
    expect(defaultRuleName("httpcheck.status", [eq("url", "/api/orders")])).toBe(
      "httpcheck.status · url=/api/orders",
    );
  });

  it("gives different names to checks that differ only by filter", () => {
    // The reported case, as a property rather than a string comparison.
    const names = ["/api/orders", "/api/invoices", "/api/health"].map((u) =>
      defaultRuleName("httpcheck.status", [eq("url", u)]),
    );
    expect(new Set(names).size).toBe(names.length);
  });

  it("summarises beyond the first couple rather than growing without bound", () => {
    const many = [eq("url", "/a"), eq("env", "prod"), eq("region", "eu"), eq("tier", "1")];
    expect(defaultRuleName("m", many)).toBe("m · url=/a, env=prod +2");
  });

  it("spells out a non-equality operator", () => {
    expect(defaultRuleName("m", [{ key: "url", op: "contains", value: "orders" }])).toBe(
      "m · url contains orders",
    );
  });

  it("ignores half-filled filters", () => {
    // The editor creates a blank row before the user types into it; a
    // name must not be seeded from one.
    expect(defaultRuleName("m", [{ key: "", op: "eq", value: "" }])).toBe("m alert");
    expect(defaultRuleName("m", [{ key: "url", op: "eq", value: "" }])).toBe("m alert");
  });

  it("does not truncate values, so two long filters stay distinct", () => {
    const a = defaultRuleName("m", [eq("url", "/very/long/shared/prefix/orders")]);
    const b = defaultRuleName("m", [eq("url", "/very/long/shared/prefix/invoices")]);
    expect(a).not.toBe(b);
  });
});
