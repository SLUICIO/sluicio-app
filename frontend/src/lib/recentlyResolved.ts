// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Health checks that fired and have since recovered on their own.
//
// A notification says something broke. By the time it is read the check
// may have cleared itself, and following the link landed on an Errors
// page reporting "All clear" — which is true, and useless: it neither
// confirms what the notification was about nor shows that it ended. The
// reader is left unsure whether they missed something or whether the
// alert was noise.
//
// So the page also lists what recovered inside the current window. It is
// deliberately separate from what is failing NOW: these need no action,
// and mixing them would inflate the counts the page exists to report.

import type { AlertInstance } from "../api/types";

const UNIT_MS: Record<string, number> = {
  m: 60 * 1000,
  h: 60 * 60 * 1000,
  d: 24 * 60 * 60 * 1000,
};

/**
 * Milliseconds in a window string like "15m", "6h", "7d".
 *
 * Returns null for anything unrecognised — an absolute range, say —
 * which callers treat as "do not filter by age" rather than guessing a
 * duration and hiding rows the user asked to see.
 */
export function windowMs(window: string): number | null {
  const m = /^(\d+)([mhd])$/.exec(window.trim());
  if (!m) return null;
  return Number(m[1]) * UNIT_MS[m[2]];
}

/**
 * The instances that resolved within the window, most recent first.
 *
 * An instance still firing is not included — it belongs to the live
 * sections. Nor is one that resolved before the window: the page is a
 * view of a period, and a recovery from last week is not part of it.
 */
export function recentlyResolved(
  instances: AlertInstance[],
  window: string,
  now = Date.now(),
): AlertInstance[] {
  const span = windowMs(window);
  const floor = span === null ? null : now - span;
  return instances
    .filter((i) => i.state === "resolved" && i.ended_at)
    .filter((i) => {
      const ended = Date.parse(i.ended_at!);
      if (Number.isNaN(ended)) return false;
      return floor === null || ended >= floor;
    })
    .sort((a, b) => Date.parse(b.ended_at!) - Date.parse(a.ended_at!));
}
