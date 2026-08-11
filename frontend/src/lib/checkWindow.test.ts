// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The failure this guards against is a warning that cries wolf: if a
// 7d window on 14d retention warns, people stop reading the warnings
// and miss the 30d-on-14d case that actually matters.

import { describe, expect, it } from "vitest";
import { windowRetentionWarning } from "./checkWindow";

const DAY = 86400;

describe("windowRetentionWarning", () => {
  it("says nothing when the window fits inside retention", () => {
    expect(windowRetentionWarning(7 * DAY, 14, true)).toBeNull();
  });

  it("says nothing when the window exactly equals retention", () => {
    // Equal is fine: the window's oldest second is retention's oldest
    // second. Warning here would flag the common "30d window, 30d
    // retention" setup, which is correct.
    expect(windowRetentionWarning(30 * DAY, 30, true)).toBeNull();
  });

  it("warns when a 30d window runs on the default 14d retention", () => {
    const w = windowRetentionWarning(30 * DAY, 14, true)!;
    expect(w).not.toBeNull();
    expect(w.windowDays).toBe(30);
    expect(w.retentionDays).toBe(14);
  });

  it("tells a dead-man's-switch it will fire on a healthy flow", () => {
    // The direction is the point. A drought check under-counts, so the
    // consequence is a false alarm, not a missed one.
    const w = windowRetentionWarning(45 * DAY, 14, true)!;
    expect(w.message).toMatch(/fire on a flow that is working/);
  });

  it("tells a flood check it may stay quiet through a breach", () => {
    const w = windowRetentionWarning(45 * DAY, 14, false)!;
    expect(w.message).toMatch(/quiet through a breach/);
  });

  it("names the retention figure so the user can act on it", () => {
    expect(windowRetentionWarning(30 * DAY, 14, true)!.message).toContain("14 days");
    expect(windowRetentionWarning(30 * DAY, 1, true)!.message).toContain("1 day");
  });

  it("stays silent when retention is unknown", () => {
    // A settings fetch that failed is not evidence of a misconfiguration,
    // and guessing here would warn on every check on every cell that
    // does not answer.
    expect(windowRetentionWarning(45 * DAY, null, true)).toBeNull();
    expect(windowRetentionWarning(45 * DAY, undefined, true)).toBeNull();
    expect(windowRetentionWarning(45 * DAY, 0, true)).toBeNull();
  });

  it("ignores a nonsensical window rather than warning about it", () => {
    expect(windowRetentionWarning(0, 14, true)).toBeNull();
    expect(windowRetentionWarning(Number.NaN, 14, true)).toBeNull();
  });
});
