// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// A metric check whose filter leaves several series in the group, read
// by an aggregation that picks one sample by timestamp, does not have a
// well-defined value. It reads whichever series won an arbitrary tie.
//
// This is not hypothetical. The httpcheck receiver emits one point per
// HTTP status class per endpoint per scrape — five points sharing one
// timestamp, value 1 on the class that matched and 0 on the other four.
// A rule filtered to one URL but not one class therefore reads 1 or 0
// depending on nothing at all, and a "status ≠ 0" rule built that way
// alarms when the site is UP.
//
// The builder can see this coming: the preview already reports how many
// series the filter matched.

/** Aggregations that select one sample by timestamp, so ties matter. */
const PICKS_BY_TIMESTAMP = new Set(["last", "age"]);

export function aggregationPicksBySample(aggregation: string): boolean {
  return PICKS_BY_TIMESTAMP.has(aggregation);
}

export interface AmbiguousSeriesWarning {
  seriesCount: number;
  /** What the reading actually means right now. */
  message: string;
  /** The concrete way out. */
  fix: string;
}

/**
 * Warn when a point-in-time aggregation is reading across several series.
 *
 * Returns null when the aggregation combines every sample (max, avg,
 * sum, p95 — a group is then a deliberate choice, not a hazard), when
 * the group holds one series or none, or when the server sent no count.
 *
 * `splitBy` disarms the warning: a split-by rule evaluates each value
 * separately, which is the other correct answer to the same problem.
 */
export function ambiguousSeriesWarning(
  aggregation: string,
  seriesCount: number | null | undefined,
  splitBy?: string | null,
): AmbiguousSeriesWarning | null {
  if (!aggregationPicksBySample(aggregation)) return null;
  if (splitBy) return null;
  if (typeof seriesCount !== "number" || !Number.isFinite(seriesCount)) return null;
  if (seriesCount <= 1) return null;

  const agg = aggregation === "age" ? "age" : "last";
  return {
    seriesCount,
    message: `This filter matches ${seriesCount} separate series, and “${agg}” reads the newest sample by timestamp. When those series report at the same moment the tie is broken arbitrarily, so the value shown is whichever series happened to win — not a reading you chose.`,
    fix: "Narrow the filter until it matches one series, split by the attribute that separates them, or use an aggregation that combines them (max, min, avg).",
  };
}
