// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A health check's trailing window is only as real as the telemetry
// still on disk behind it. Retention is a per-org setting that defaults
// to 14 days, and the window now runs to 45, so the two can disagree —
// silently, because ClickHouse does not report "you asked for 30 days
// and I have 14", it just returns a smaller count.
//
// That silence is the whole reason this file exists. The count is wrong
// in a DIRECTION, and the direction depends on which way the check
// compares, so the warning has to say which failure the user is about
// to configure rather than stating the mismatch and leaving them to
// work it out.

/** Seconds in a day, for turning a window into something readable. */
const DAY = 86400;

export interface WindowRetentionWarning {
  /** The configured window, rounded down to whole days. */
  windowDays: number;
  /** Retention for the telemetry this check reads. */
  retentionDays: number;
  /** What goes wrong, in terms of this check's own behaviour. */
  message: string;
}

/**
 * Warn when a check's window reaches past its telemetry's retention.
 *
 * `firesBelow` is the check's direction: true for a dead-man's-switch
 * ("fewer than N"), false for a flood ("at least N"). It decides which
 * way the truncated count is wrong, and the two are not equally bad:
 *
 *   - A drought check under-counts, so it fires on a healthy flow. The
 *     user gets a false alarm every window, which is the failure that
 *     teaches people to switch checks off.
 *   - A flood check also under-counts, so it can sit quiet through a
 *     real breach. Quieter, and worse.
 *
 * Returns null when the window fits, when retention is unknown (a
 * missing setting is not evidence of a problem), or when the numbers
 * are not usable.
 */
export function windowRetentionWarning(
  windowSeconds: number,
  retentionDays: number | null | undefined,
  firesBelow: boolean,
): WindowRetentionWarning | null {
  if (!retentionDays || retentionDays <= 0) return null;
  if (!Number.isFinite(windowSeconds) || windowSeconds <= 0) return null;
  if (windowSeconds <= retentionDays * DAY) return null;

  const windowDays = Math.floor(windowSeconds / DAY);
  const kept = `${retentionDays} day${retentionDays === 1 ? "" : "s"}`;
  const message = firesBelow
    ? `This window is longer than the ${kept} of telemetry this cell keeps, so the count can never cover it. The check would fire on a flow that is working.`
    : `This window is longer than the ${kept} of telemetry this cell keeps, so anything older than that is never counted. The check can stay quiet through a breach it should catch.`;

  return { windowDays, retentionDays, message };
}
