// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// These cases are the SAME table as TestHumanizeKey in the Go package.
// Keeping them identical is the point: the server fills a blank label
// with its own version, so a drift between the two produces two
// different headings for one attribute depending on which path created
// the column.

import { describe, expect, it } from "vitest";
import { humanizeAttributeKey } from "./humanizeAttributeKey";

describe("humanizeAttributeKey", () => {
  it.each([
    // Two segments: both kept. Dropping the first would give "Exported".
    ["documents.exported", "Documents exported"],
    ["archive.month_from", "Archive month from"],
    // Three or more: the leading namespace is noise in context.
    ["node_red.flow.name", "Flow name"],
    ["http.response.status", "Response status"],
    // Nothing to drop.
    ["count", "Count"],
    // A trailing separator must not leave a dangling dot.
    ["weird.", "Weird"],
    // Already readable stays readable.
    ["Documents exported", "Documents exported"],
    ["", ""],
  ] as const)("%s → %s", (input, want) => {
    expect(humanizeAttributeKey(input)).toBe(want);
  });

  it("trims surrounding whitespace like the server does", () => {
    expect(humanizeAttributeKey("  documents.exported  ")).toBe("Documents exported");
  });

  it("collapses runs of separators rather than emitting double spaces", () => {
    expect(humanizeAttributeKey("a__b")).toBe("A b");
  });
});
