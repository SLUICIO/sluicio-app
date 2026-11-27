// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, expect, it } from "vitest";
import { hasChanges, lineDiff } from "./lineDiff";

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
