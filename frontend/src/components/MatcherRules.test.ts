// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// "Include child spans" is a rule property that the wire stores per
// matcher row and the backend reads per match group. The translation in
// both directions has to agree, or a saved rule reopens without it.

import { describe, expect, it } from "vitest";
import { matchersToRules, rulesPreview, rulesToMatchers, type Rule } from "./MatcherRules";

const rule = (patch: Partial<Rule>): Rule => ({
  serviceOp: "equals",
  service: "order-gateway",
  combine: "any",
  attrs: [
    { attribute: "abc", operator: "equals", value: "123" },
    { attribute: "def", operator: "equals", value: "456" },
  ],
  ...patch,
});

describe("include child spans", () => {
  it("marks every row of every group the rule expands into", () => {
    const rows = rulesToMatchers([rule({ descendants: true })]);
    // An "any" rule is one group per condition, each with the service row.
    expect(rows).toHaveLength(4);
    expect(rows.every((r) => r.include_descendants)).toBe(true);
  });

  it("leaves the rows alone when the rule does not ask", () => {
    const rows = rulesToMatchers([rule({})]);
    expect(rows.some((r) => "include_descendants" in r)).toBe(false);
  });

  it("round-trips for both combine modes", () => {
    for (const combine of ["any", "all"] as const) {
      const rows = rulesToMatchers([rule({ combine, descendants: true })]);
      const [back] = matchersToRules(rows);
      expect(back.descendants).toBe(true);
      expect(back.combine).toBe(combine);
    }
  });

  it("does not leak into a neighbouring rule", () => {
    const rows = rulesToMatchers([
      rule({ descendants: true }),
      rule({ service: "order-processor", attrs: [{ attribute: "x", operator: "exists", value: "" }] }),
    ]);
    const back = matchersToRules(rows);
    expect(back.map((r) => !!r.descendants)).toEqual([true, false]);
  });

  it("says so in the preview", () => {
    expect(rulesPreview([rule({ descendants: true })])).toContain("+ child spans");
    expect(rulesPreview([rule({})])).not.toContain("child spans");
  });
});
