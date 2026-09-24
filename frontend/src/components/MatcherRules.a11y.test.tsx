// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The editor's controls need names of their own. They had none, so the
// e2e suite reached the operator by position - "the first control on the
// page" - and every change to the layout silently pointed three tests at
// a different control. A pill's visible text is its VALUE and changes as
// somebody edits it, so the name has to be separate.

import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import MatcherRules, { blankRule, type Rule } from "./MatcherRules";

const rules: Rule[] = [
  { ...blankRule({ service: "svc-a" }), attrs: [{ attribute: "a", operator: "equals", value: "1" }] },
];

describe("MatcherRules accessible names", () => {
  it("names every control, so nothing has to be found by position", () => {
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
    for (const name of [
      "How the rules combine",
      "Service match operator",
      "Service",
      "Attribute",
      "Attribute match operator",
      "Attribute value",
    ]) {
      expect(screen.getByRole("button", { name }), `missing control: ${name}`).toBeTruthy();
    }
  });

  it("shows the rule's values on the pills, so it reads as a sentence", () => {
    render(
      <MatcherRules rules={rules} onChange={() => {}} knownServices={["svc-a"]} attrKeys={["a"]} />,
    );
    expect(screen.getByRole("button", { name: "Service" }).textContent).toContain("svc-a");
    expect(screen.getByRole("button", { name: "Attribute value" }).textContent).toContain("1");
  });

  // Read-only surfaces pass no handler, and then the mode control is not
  // rendered at all rather than rendered and ignored.
  it("leaves the mode out where it cannot be changed", () => {
    render(
      <MatcherRules rules={rules} onChange={() => {}} knownServices={[]} attrKeys={[]} combine="all" />,
    );
    expect(screen.queryByRole("button", { name: "How the rules combine" })).toBeNull();
  });

  // The list holds what the cell has seen in the editor's window. A
  // nightly job that ran at three in the morning is not in it, and a
  // rule you cannot write for a quiet service is a rule you cannot write
  // for the ones that matter most.
  it("lets a rule name a service the cell has not seen", async () => {
    const changes: Rule[][] = [];
    render(
      <MatcherRules
        rules={[blankRule({ serviceOp: "equals" })]}
        onChange={(r) => changes.push(r)}
        knownServices={["order-gateway"]}
        attrKeys={[]}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Service" }));
    const box = screen.getByRole("textbox", { name: "Service name" });
    await userEvent.type(box, "nightly-batch-runner{Enter}");
    expect(changes.at(-1)?.[0].service).toBe("nightly-batch-runner");
  });
});
