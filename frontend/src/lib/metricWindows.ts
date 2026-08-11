// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The evaluation-window choices for a METRIC check.
//
// Metric specs store for_window as a Go duration string, and Go
// durations have no day unit — a month is "720h". Nobody reads "720h"
// as a month, so the value and the label have to be separate. Log and
// trace checks store seconds instead and can label themselves.
//
// Shared by the metric builder and the health-check editor because the
// two were drifting: both hard-coded the same five-entry array, both
// stopped at 1h, and a check that needed a day could be written in
// neither.

export interface MetricWindowChoice {
  /** Go duration, stored verbatim in spec.for_window. */
  value: string;
  /** What the user reads. */
  label: string;
}

/**
 * Capped at 1080h (45d) to match alerting.maxCheckWindow — anything
 * longer is silently clamped server-side, and a picker that offers a
 * value the server will quietly change is worse than one that stops.
 */
export const METRIC_WINDOWS: MetricWindowChoice[] = [
  { value: "1m", label: "1m" },
  { value: "5m", label: "5m" },
  { value: "10m", label: "10m" },
  { value: "30m", label: "30m" },
  { value: "1h", label: "1h" },
  { value: "6h", label: "6h" },
  { value: "12h", label: "12h" },
  { value: "24h", label: "1d" },
  { value: "48h", label: "2d" },
  { value: "168h", label: "7d" },
  { value: "336h", label: "14d" },
  { value: "720h", label: "30d" },
  { value: "1080h", label: "45d" },
];

/**
 * The label for a stored for_window, falling back to the raw duration.
 *
 * The fallback matters: rules saved before this list existed, and rules
 * written through the API, can hold any duration at all. Showing the raw
 * string is honest; showing nothing, or the nearest entry, is not.
 */
export function metricWindowLabel(value: string): string {
  return METRIC_WINDOWS.find((w) => w.value === value)?.label ?? value;
}

/** Seconds for a metric window value, for comparing against retention. */
export function metricWindowSeconds(value: string): number {
  const m = /^(\d+(?:\.\d+)?)(ms|s|m|h)$/.exec(value.trim());
  if (!m) return 0;
  const n = Number(m[1]);
  switch (m[2]) {
    case "ms":
      return n / 1000;
    case "s":
      return n;
    case "m":
      return n * 60;
    default:
      return n * 3600;
  }
}
