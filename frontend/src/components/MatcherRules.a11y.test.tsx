// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The selects on this editor need names of their own. They did not have
// any, so the e2e suite reached the operator by position - "the first
// combobox on the page" - and adding the rule-combination select above
// them silently pointed three tests at the wrong control.

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import MatcherRules, { blankRule, type Rule } from "./MatcherRules";

const rules: Rule[] = [
  { ...blankRule({ service: "svc-a" }), attrs: [{ attribute: "a", operator: "equals", value: "1" }] },
];

describe("MatcherRules accessible names", () => {
  it("names each select, so nothing has to be found by position", () => {
    render(
      <MatcherRules
        rules={rules}
        onChange={() => {}}
        knownServices={["svc-a"]}
        attrKeys={["a"]}
        combine="any"
        onCombineChange={() => {}}
      />,
    );
    expect(screen.getByRole("combobox", { name: "How the rules combine" })).toBeTruthy();
    expect(screen.getByRole("combobox", { name: "Service match operator" })).toBeTruthy();
    expect(screen.getByRole("combobox", { name: "Attribute match operator" })).toBeTruthy();
  });

  // The operator select is the one the suite drives; it must offer the
  // value those tests choose.
  it("offers the operator the suite picks", () => {
    render(
      <MatcherRules rules={rules} onChange={() => {}} knownServices={[]} attrKeys={[]} />,
    );
    const op = screen.getByRole("combobox", { name: "Service match operator" }) as HTMLSelectElement;
    expect([...op.options].map((o) => o.value)).toContain("equals");
  });

  // Read-only surfaces pass no handler, and then the mode select is not
  // rendered at all rather than rendered and ignored.
  it("leaves the mode out where it cannot be changed", () => {
    render(
      <MatcherRules rules={rules} onChange={() => {}} knownServices={[]} attrKeys={[]} combine="all" />,
    );
    expect(screen.queryByRole("combobox", { name: "How the rules combine" })).toBeNull();
  });
});
