// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { METRIC_WINDOWS, metricWindowLabel, metricWindowSeconds } from "./metricWindows";

describe("METRIC_WINDOWS", () => {
  it("only offers durations Go can parse", () => {
    // The value goes into spec.for_window verbatim and is parsed by
    // time.ParseDuration, which rejects "30d". A "d" here would be a
    // 400 on save, not a display bug.
    for (const w of METRIC_WINDOWS) {
      expect(w.value).toMatch(/^\d+(\.\d+)?(ms|s|m|h)$/);
    }
  });

  it("labels long windows in days, since nobody reads 720h as a month", () => {
    expect(METRIC_WINDOWS.find((w) => w.value === "720h")?.label).toBe("30d");
    expect(METRIC_WINDOWS.find((w) => w.value === "168h")?.label).toBe("7d");
  });

  it("stops at the server's 45d ceiling", () => {
    // Offering more would show a value the server silently clamps.
    const longest = Math.max(...METRIC_WINDOWS.map((w) => metricWindowSeconds(w.value)));
    expect(longest).toBe(45 * 86400);
  });
});

describe("metricWindowLabel", () => {
  it("labels a known window", () => {
    expect(metricWindowLabel("1080h")).toBe("45d");
  });

  it("shows an unknown duration verbatim rather than inventing one", () => {
    // API-written and pre-existing rules can hold anything; snapping to
    // the nearest entry would misreport what the rule actually does.
    expect(metricWindowLabel("90m")).toBe("90m");
  });
});

describe("metricWindowSeconds", () => {
  it.each([
    ["5m", 300],
    ["1h", 3600],
    ["720h", 2592000],
    ["30s", 30],
  ] as const)("converts %s", (v, secs) => {
    expect(metricWindowSeconds(v)).toBe(secs);
  });

  it("returns 0 for something it cannot parse, rather than guessing", () => {
    expect(metricWindowSeconds("30d")).toBe(0);
    expect(metricWindowSeconds("")).toBe(0);
  });
});
