// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import { formatDurationMinutes, summarizeRecurrence } from "./maintenanceRecurrence";

describe("summarizeRecurrence", () => {
  it("reads a weekly series", () => {
    expect(
      summarizeRecurrence({ frequency: "weekly", weekdays: [0], start_time: "02:00", duration_minutes: 120, timezone: "Europe/Stockholm" }),
    ).toBe("Every Sunday at 02:00 for 2 h (Europe/Stockholm)");
    expect(
      summarizeRecurrence({ frequency: "weekly", weekdays: [3, 1, 5], start_time: "22:30", duration_minutes: 90, timezone: "UTC", ends_on: "2027-01-31" }),
    ).toBe("Every Monday, Wednesday and Friday at 22:30 for 1 h 30 min (UTC), until 2027-01-31");
  });

  it("reads a monthly series", () => {
    expect(
      summarizeRecurrence({ frequency: "monthly", month_week: 2, month_weekday: 2, start_time: "20:00", duration_minutes: 240, timezone: "UTC" }),
    ).toBe("The second Tuesday of each month at 20:00 for 4 h (UTC)");
    expect(
      summarizeRecurrence({ frequency: "monthly", month_week: -1, month_weekday: 5, start_time: "18:00", duration_minutes: 1440, timezone: "UTC" }),
    ).toBe("The last Friday of each month at 18:00 for 1 day (UTC)");
  });

  it("formats durations", () => {
    expect(formatDurationMinutes(45)).toBe("45 min");
    expect(formatDurationMinutes(2880)).toBe("2 days");
  });
});
