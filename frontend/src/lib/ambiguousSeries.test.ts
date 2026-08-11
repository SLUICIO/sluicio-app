// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Pinned against the real rule that motivated this: last(httpcheck.status)
// ≠ 0 filtered to one URL but not one status class, which fired 69 times
// in a day on a site that never went down.

import { describe, expect, it } from "vitest";
import { aggregationPicksBySample, ambiguousSeriesWarning } from "./ambiguousSeries";

describe("aggregationPicksBySample", () => {
  it("flags the aggregations that choose a sample by timestamp", () => {
    expect(aggregationPicksBySample("last")).toBe(true);
    expect(aggregationPicksBySample("age")).toBe(true);
  });

  it("does not flag aggregations that choose by value or combine", () => {
    // max/min also return one sample's value, but they pick it by VALUE,
    // which is well-defined across a group. Flagging them would warn on
    // most correct rules.
    for (const a of ["max", "min", "avg", "sum", "p95", "increase", "rate"]) {
      expect(aggregationPicksBySample(a)).toBe(false);
    }
  });
});

describe("ambiguousSeriesWarning", () => {
  it("warns on the httpcheck shape: last over five tied series", () => {
    const w = ambiguousSeriesWarning("last", 5)!;
    expect(w).not.toBeNull();
    expect(w.seriesCount).toBe(5);
    expect(w.message).toContain("5 separate series");
    expect(w.fix).toMatch(/Narrow the filter/);
  });

  it("says nothing when the filter matches exactly one series", () => {
    expect(ambiguousSeriesWarning("last", 1)).toBeNull();
  });

  it("says nothing when nothing matched at all", () => {
    // An empty group is a "no data" problem the preview already reports;
    // two warnings for one cause is noise.
    expect(ambiguousSeriesWarning("last", 0)).toBeNull();
  });

  it("leaves combining aggregations alone even across many series", () => {
    // max over 5 series is exactly what a fleet-wide check wants.
    expect(ambiguousSeriesWarning("max", 5)).toBeNull();
    expect(ambiguousSeriesWarning("avg", 40)).toBeNull();
  });

  it("is disarmed by split-by, which evaluates each series separately", () => {
    expect(ambiguousSeriesWarning("last", 5, "http.status_class")).toBeNull();
  });

  it("stays silent when the server sent no count", () => {
    // Older cells and failed count queries both omit the field. Absence
    // is not evidence of ambiguity.
    expect(ambiguousSeriesWarning("last", undefined)).toBeNull();
    expect(ambiguousSeriesWarning("last", null)).toBeNull();
    expect(ambiguousSeriesWarning("last", Number.NaN)).toBeNull();
  });

  it("names the aggregation the user actually picked", () => {
    expect(ambiguousSeriesWarning("age", 3)!.message).toContain("age");
    expect(ambiguousSeriesWarning("last", 3)!.message).toContain("last");
  });
});
