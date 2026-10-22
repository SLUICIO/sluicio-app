// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Plain-language summaries of a recurring maintenance window (issue #9),
// so a series reads as "Every Sunday at 02:00 for 2 h" rather than as
// the JSON it is stored as.

import type { MaintenanceRecurrence } from "../api/types";

export const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const ORDINALS: Record<number, string> = { 1: "first", 2: "second", 3: "third", 4: "fourth", [-1]: "last" };

export function formatDurationMinutes(min: number): string {
  if (min % (24 * 60) === 0) {
    const d = min / (24 * 60);
    return `${d} day${d === 1 ? "" : "s"}`;
  }
  if (min % 60 === 0) return `${min / 60} h`;
  if (min < 60) return `${min} min`;
  return `${Math.floor(min / 60)} h ${min % 60} min`;
}

function joinDays(days: string[]): string {
  if (days.length <= 1) return days.join("");
  return `${days.slice(0, -1).join(", ")} and ${days[days.length - 1]}`;
}

export function summarizeRecurrence(r: MaintenanceRecurrence): string {
  let when: string;
  if (r.frequency === "weekly") {
    const days = [...(r.weekdays ?? [])].sort((a, b) => a - b);
    when = days.length === 7 ? "Every day" : `Every ${joinDays(days.map((d) => WEEKDAYS[d]))}`;
  } else {
    when = `The ${ORDINALS[r.month_week ?? 1] ?? "?"} ${WEEKDAYS[r.month_weekday ?? 0]} of each month`;
  }
  let out = `${when} at ${r.start_time} for ${formatDurationMinutes(r.duration_minutes)} (${r.timezone})`;
  if (r.ends_on) out += `, until ${r.ends_on}`;
  return out;
}

/** The browser's own timezone, the sensible default for a new series. */
export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

/** Every IANA timezone the browser knows, for a picker. Empty when the
 *  browser cannot list them; the field then takes free text, which the
 *  server validates. */
export function knownTimezones(): string[] {
  const intl = Intl as unknown as { supportedValuesOf?: (k: string) => string[] };
  try {
    return intl.supportedValuesOf ? intl.supportedValuesOf("timeZone") : [];
  } catch {
    return [];
  }
}
