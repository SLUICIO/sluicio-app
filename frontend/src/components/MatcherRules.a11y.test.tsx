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
import { RuleEditor, blankRule, type Rule } from "./MatcherRules";

const rule: Rule = {
  ...blankRule({ service: "svc-a" }),
  attrs: [
    { attribute: "a", operator: "equals", value: "1" },
    { attribute: "b", operator: "equals", value: "2" },
  ],
};

describe("RuleEditor accessible names", () => {
  it("names every control, so nothing has to be found by position", () => {
    render(<RuleEditor rule={rule} onChange={() => {}} knownServices={["svc-a"]} attrKeys={["a"]} />);
    for (const name of [
      "Service match operator",
      "Service",
      "How the conditions combine",
      "Attribute match operator",
      "Attribute value",
    ]) {
      expect(screen.getAllByRole("button", { name })[0], `missing control: ${name}`).toBeTruthy();
    }
    expect(screen.getAllByRole("button", { name: "Attribute" })).toHaveLength(2);
    expect(screen.getAllByRole("button", { name: "Remove condition" })).toHaveLength(2);
  });

  it("shows the rule's values on the pills, so it reads as a sentence", () => {
    render(<RuleEditor rule={rule} onChange={() => {}} knownServices={["svc-a"]} attrKeys={["a"]} />);
    expect(screen.getByRole("button", { name: "Service" }).textContent).toContain("svc-a");
    expect(screen.getAllByRole("button", { name: "Attribute value" })[0].textContent).toContain("1");
  });

  // Read-only surfaces show the rule and nothing to act on.
  it("offers nothing to change when locked", () => {
    render(<RuleEditor rule={rule} onChange={() => {}} knownServices={[]} attrKeys={[]} locked />);
    expect(screen.queryByRole("button", { name: "Remove condition" })).toBeNull();
    expect(screen.queryByRole("button", { name: "+ condition" })).toBeNull();
    expect(screen.queryByRole("button", { name: /child spans/ })).toBeNull();
    expect(screen.getByRole("button", { name: "Service" }).getAttribute("aria-disabled")).toBe("true");
  });

  // The list holds what the cell has seen in the editor's window. A
  // nightly job that ran at three in the morning is not in it, and a
  // rule you cannot write for a quiet service is a rule you cannot write
  // for the ones that matter most.
  it("lets a rule name a service the cell has not seen", async () => {
    const changes: Rule[] = [];
    render(
      <RuleEditor
        rule={blankRule({ serviceOp: "equals" })}
        onChange={(r) => changes.push(r)}
        knownServices={["order-gateway"]}
        attrKeys={[]}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Service" }));
    const box = screen.getByRole("textbox", { name: "Service name" });
    await userEvent.type(box, "nightly-batch-runner{Enter}");
    expect(changes.at(-1)?.service).toBe("nightly-batch-runner");
  });
});
