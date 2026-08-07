// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The point of this list is to answer "was that the thing I was told
// about, and is it over?" — so it must show what recovered, never
// something still broken, and never quietly drop a row it could not
// parse.

import { describe, expect, it } from "vitest";
import { recentlyResolved, windowMs } from "./recentlyResolved";
import type { AlertInstance } from "../api/types";

const now = Date.parse("2026-08-07T12:00:00Z");
const at = (msAgo: number) => new Date(now - msAgo).toISOString();

const inst = (over: Partial<AlertInstance>): AlertInstance =>
  ({
    id: Math.random().toString(36).slice(2),
    alert_rule_id: "r",
    rule_name: "check",
    severity: "warning",
    state: "resolved",
    started_at: at(60 * 60 * 1000),
    ...over,
  }) as AlertInstance;

describe("windowMs", () => {
  it("reads the window vocabulary the picker uses", () => {
    expect(windowMs("15m")).toBe(15 * 60 * 1000);
    expect(windowMs("6h")).toBe(6 * 60 * 60 * 1000);
    expect(windowMs("7d")).toBe(7 * 24 * 60 * 60 * 1000);
  });

  it("returns null rather than guessing at something it cannot read", () => {
    // Treated by callers as "do not filter by age" — hiding rows because
    // a window was unparseable would be the worse failure.
    expect(windowMs("2026-08-01..2026-08-02")).toBeNull();
    expect(windowMs("")).toBeNull();
  });
});

describe("recentlyResolved", () => {
  it("lists what recovered inside the window, newest first", () => {
    const out = recentlyResolved(
      [
        inst({ rule_name: "older", ended_at: at(50 * 60 * 1000) }),
        inst({ rule_name: "newer", ended_at: at(5 * 60 * 1000) }),
      ],
      "6h",
      now,
    );
    expect(out.map((i) => i.rule_name)).toEqual(["newer", "older"]);
  });

  it("never includes something still firing", () => {
    // Those belong to the live sections; showing them here would say a
    // live incident is over.
    const out = recentlyResolved([inst({ state: "firing", ended_at: undefined })], "6h", now);
    expect(out).toHaveLength(0);
  });

  it("excludes a recovery from before the window", () => {
    expect(recentlyResolved([inst({ ended_at: at(8 * 60 * 60 * 1000) })], "6h", now)).toHaveLength(0);
  });

  it("keeps everything resolved when the window cannot be parsed", () => {
    const out = recentlyResolved([inst({ ended_at: at(30 * 24 * 60 * 60 * 1000) })], "custom", now);
    expect(out).toHaveLength(1);
  });

  it("drops a row whose end time is unreadable rather than sorting on NaN", () => {
    // One bad timestamp must not scramble the order of the rest.
    const out = recentlyResolved(
      [inst({ ended_at: "not-a-date" }), inst({ rule_name: "good", ended_at: at(60 * 1000) })],
      "6h",
      now,
    );
    expect(out.map((i) => i.rule_name)).toEqual(["good"]);
  });
});
