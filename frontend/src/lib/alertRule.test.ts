// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, it, expect } from "vitest";
import type { AlertRule } from "../api/types";
import { fmtWindow, logSevLabel, alertCondition, alertSignalLabel } from "./alertRule";

// Test rules only need the fields the renderer reads; cast through
// unknown so we don't have to spell out the whole AlertRule shape.
const rule = (r: Record<string, unknown>) => r as unknown as AlertRule;

describe("fmtWindow", () => {
  it.each([
    [3600, "1h"],
    [7200, "2h"],
    [300, "5m"],
    [90, "90s"],
  ] as const)("renders %i seconds as %s", (secs, out) => {
    expect(fmtWindow(secs)).toBe(out);
  });
});

describe("logSevLabel", () => {
  it.each([
    [21, "fatal"],
    [17, "error"],
    [13, "warn"],
    [5, "≥5"],
    [0, "any"],
  ] as const)("maps severity floor %i → %s", (n, label) => {
    expect(logSevLabel(n)).toBe(label);
  });
});

describe("alertCondition", () => {
  it("renders a failed-trace rule", () => {
    expect(
      alertCondition(rule({ signal: "trace", trace_error_spec: { threshold: 3, window_seconds: 300 } })),
    ).toBe("≥3 failed traces · 5m");
  });

  it("singularises a threshold of one", () => {
    expect(
      alertCondition(rule({ signal: "trace", trace_error_spec: { threshold: 1, window_seconds: 60 } })),
    ).toBe("≥1 failed trace · 1m");
  });

  it("renders a latency rule with its aggregation", () => {
    expect(
      alertCondition(
        rule({ signal: "trace", trace_latency_spec: { aggregation: "p95", threshold_ms: 500, window_seconds: 60 } }),
      ),
    ).toBe("p95 response time ≥500ms · 1m");
  });

  it("renders a log flood rule with severity and body match", () => {
    expect(
      alertCondition(
        rule({
          signal: "log",
          log_spec: { comparison: "at_least", threshold: 10, min_severity: 17, body_contains: "timeout", window_seconds: 600 },
        }),
      ),
    ).toBe('≥10 logs · sev error · contains "timeout" · 10m');
  });
});

describe("alertSignalLabel", () => {
  it("badges log and trace signals, but not metric", () => {
    expect(alertSignalLabel("log")).toBe("log");
    expect(alertSignalLabel("trace")).toBe("trace");
    expect(alertSignalLabel("metric")).toBeNull();
  });
});
