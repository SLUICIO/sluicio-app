// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, expect, it } from "vitest";
import { hasChanges, lineDiff, unifiedDiff } from "./lineDiff";

describe("lineDiff", () => {
  it("marks a changed line as removed and added, the rest unchanged", () => {
    const d = lineDiff("a: 1\nb: 2\nc: 3\n", "a: 1\nb: 5\nc: 3\nd: 4\n");
    expect(d).toEqual([
      { op: " ", text: "a: 1" },
      { op: "-", text: "b: 2" },
      { op: "+", text: "b: 5" },
      { op: " ", text: "c: 3" },
      { op: "+", text: "d: 4" },
    ]);
    expect(hasChanges(d)).toBe(true);
  });
  it("finds nothing between equal texts", () => {
    expect(hasChanges(lineDiff("x: 1\n", "x: 1\n"))).toBe(false);
  });
});

describe("unifiedDiff", () => {
  it("writes a patch with hunks and context", () => {
    const before = ["a", "b", "c", "d", "e", "f", "g", "h", "i", "j"].join("\n") + "\n";
    const after = ["a", "b", "c", "D", "e", "f", "g", "h", "i", "j", "k"].join("\n") + "\n";
    expect(unifiedDiff(before, after, "collector.yaml", 1)).toBe(
      [
        "--- a/collector.yaml",
        "+++ b/collector.yaml",
        "@@ -3,3 +3,3 @@",
        " c",
        "-d",
        "+D",
        " e",
        "@@ -10,1 +10,2 @@",
        " j",
        "+k",
        "",
      ].join("\n"),
    );
  });

  it("is empty without a change", () => {
    expect(unifiedDiff("a\n", "a\n", "x.yaml")).toBe("");
  });
});
