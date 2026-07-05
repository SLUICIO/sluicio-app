// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, it, expect, vi, afterEach } from "vitest";
import { formatDurationMs, statusLabel, formatRelative } from "./format";

describe("formatDurationMs", () => {
  it("shows sub-millisecond values with 2 decimals", () => {
    expect(formatDurationMs(0.5)).toBe("0.50 ms");
  });
  it("shows whole milliseconds below a second", () => {
    expect(formatDurationMs(42)).toBe("42 ms");
    expect(formatDurationMs(999)).toBe("999 ms");
  });
  it("switches to seconds at and above 1000ms", () => {
    expect(formatDurationMs(1000)).toBe("1.00 s");
    expect(formatDurationMs(2500)).toBe("2.50 s");
  });
});

describe("statusLabel", () => {
  it.each([
    ["ok", "Receiving data"],
    ["errors", "Errors detected"],
    ["quiet", "Quiet"],
    ["unhealthy", "Unhealthy"],
  ] as const)("labels %s", (status, label) => {
    expect(statusLabel(status)).toBe(label);
  });
});

describe("formatRelative", () => {
  // Pin "now" so the relative math is deterministic.
  afterEach(() => vi.useRealTimers());
  const now = new Date("2026-06-20T12:00:00.000Z");
  const ago = (seconds: number) => new Date(now.getTime() - seconds * 1000).toISOString();

  it.each([
    [2, "just now"],
    [30, "30 seconds ago"],
    [120, "2 min ago"],
    [3 * 3600, "3 h ago"],
    [2 * 86400, "2 d ago"],
  ] as const)("renders %i seconds ago as %s", (seconds, expected) => {
    vi.useFakeTimers();
    vi.setSystemTime(now);
    expect(formatRelative(ago(seconds))).toBe(expected);
  });
});
