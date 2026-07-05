// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, it, expect } from "vitest";
import { severityBand, levelBadgeLabel } from "./severity";

describe("severityBand", () => {
  // The contract is the OTel severity_number → 5-bucket mapping:
  // 1–8 debug, 9–12 info, 13–16 warn, 17–20 err, 21–24 fatal.
  it.each([
    [1, "debug"],
    [8, "debug"],
    [9, "info"],
    [12, "info"],
    [13, "warn"],
    [16, "warn"],
    [17, "err"],
    [20, "err"],
    [21, "fatal"],
    [24, "fatal"],
  ] as const)("maps severity %i → %s", (num, band) => {
    expect(severityBand(num)).toBe(band);
  });

  it("clamps below-range numbers to debug", () => {
    expect(severityBand(0)).toBe("debug");
  });
});

describe("levelBadgeLabel", () => {
  it.each([
    [5, "DBUG"],
    [10, "INFO"],
    [14, "WARN"],
    [18, "ERR"],
    [22, "FATAL"],
  ] as const)("labels severity %i as %s", (num, label) => {
    expect(levelBadgeLabel(num)).toBe(label);
  });
});
