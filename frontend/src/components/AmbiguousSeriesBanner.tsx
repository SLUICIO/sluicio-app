// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// "This reading is arbitrary." Shown when a point-in-time aggregation
// (last, age) is pooling several series, which makes the previewed
// value a tie-break rather than a measurement. See lib/ambiguousSeries
// for why, and for the real rule that motivated it.
//
// Shared by both rule builders — the metrics-page one and the
// health-check editor — because both can express the mistake.

import { ambiguousSeriesWarning } from "../lib/ambiguousSeries";

export default function AmbiguousSeriesBanner({
  aggregation,
  series,
  splitBy,
}: {
  aggregation: string;
  series?: number;
  /** Omit where the editor offers no split-by; the hint then drops it. */
  splitBy?: string;
}) {
  const warn = ambiguousSeriesWarning(aggregation, series, splitBy);
  if (!warn) return null;
  return (
    <div className="alert alert--warn" style={{ margin: 0, fontSize: 12.5, lineHeight: 1.5 }}>
      <div style={{ fontWeight: 600, marginBottom: 3 }}>This reading is not well defined</div>
      <div>{warn.message}</div>
      <div className="muted" style={{ marginTop: 4 }}>{warn.fix}</div>
    </div>
  );
}
