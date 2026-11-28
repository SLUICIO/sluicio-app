// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { collectorShare, effectSummary } from "./deploymentEffect";

const w = (from: string, to: string, rows: number, per_hour: number) => ({
  from,
  to,
  rows,
  per_hour,
});
const applied = "2026-11-27T12:00:00Z";
const effect = (before: number, after: number, change_pct?: number) => ({
  subject: "system.cpu.load_average.15m",
  applied: true,
  effect: {
    applied_at: applied,
    ready: true,
    before: w("2026-11-27T10:00:00Z", applied, before * 2, before),
    after: w(applied, "2026-11-27T14:00:00Z", after * 2, after),
    change_pct,
  },
});

describe("effectSummary", () => {
  it("says when a metric stopped arriving", () => {
    expect(effectSummary(effect(1200, 0, -100))).toBe(
      "system.cpu.load_average.15m: 1,200/h before, 0/h after (2 h each side), -100%: it stopped arriving.",
    );
  });

  it("says a metric that still arrives probably comes another way", () => {
    expect(effectSummary(effect(1200, 600, -50))).toMatch(
      /-50%\. It still arrives, probably through another collector/,
    );
  });

  it("waits until there is something to measure", () => {
    const e = effect(1, 1, 0);
    e.effect.ready = false;
    expect(effectSummary(e)).toMatch(/measured from 15 minutes after/);
    expect(effectSummary({ subject: "m", applied: false })).toBe("");
  });

  it("names a log floor as its subject", () => {
    const e = {
      ...effect(900, 0, -100),
      subject: "orders-api's logs below WARN",
    };
    expect(effectSummary(e)).toMatch(
      /^orders-api's logs below WARN: 900\/h before, 0\/h after .*it stopped arriving\.$/,
    );
  });

  it("does not invent a percentage from nothing", () => {
    expect(effectSummary(effect(0, 0))).toMatch(
      /It was not arriving before the change\.$/,
    );
  });

  it("adds this collector's share when it stamps its data", () => {
    const e = {
      ...effect(1200, 600, -50),
      collector_effect: effect(600, 0, -100).effect,
    };
    expect(collectorShare(e)).toBe(
      "Through this collector: 600/h before, 0/h after, it stopped.",
    );
    expect(collectorShare(effect(1, 1, 0))).toBe("");
  });
});
