// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The property: the picker offers every aggregation the server accepts.
//
// It didn't. The health-check editor's list was a hand-written subset
// missing `last` and `age`, so a check on a point-in-time gauge — an
// HTTP status code, a queue depth — could not be expressed at all, even
// though the server had supported it all along and shipped templates
// using it.
//
// The set below must stay in step with alerting.ValidAggregation in
// services/cell-api/internal/alerting/types.go. Spelling it out here
// means adding one server-side without surfacing it fails a test rather
// than quietly leaving a function unreachable from the UI.

import { describe, expect, it } from "vitest";
import { AGG_LABELS, ALERT_AGGREGATIONS } from "./aggregations";

const SERVER_ACCEPTS = ["last", "max", "avg", "min", "sum", "p95", "increase", "rate", "age"];

describe("ALERT_AGGREGATIONS", () => {
  it("offers exactly what the server accepts", () => {
    expect([...ALERT_AGGREGATIONS].sort()).toEqual([...SERVER_ACCEPTS].sort());
  });

  it("includes the two that were missing from the health-check editor", () => {
    // Named explicitly: `last` is the only correct function for a gauge
    // (httpcheck.status = 200), and `age` is what staleness checks need.
    expect(ALERT_AGGREGATIONS).toContain("last");
    expect(ALERT_AGGREGATIONS).toContain("age");
  });

  it("labels every option it offers", () => {
    // An unlabelled option renders as blank in the select.
    for (const a of ALERT_AGGREGATIONS) {
      expect(AGG_LABELS[a], `no label for ${a}`).toBeTruthy();
    }
  });

  it("offers each option once", () => {
    expect(new Set(ALERT_AGGREGATIONS).size).toBe(ALERT_AGGREGATIONS.length);
  });

  it("leads with last, the right default for a gauge", () => {
    expect(ALERT_AGGREGATIONS[0]).toBe("last");
  });
});
