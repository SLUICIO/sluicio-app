// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// One metric row of the explorer table — shared by the flat list and the
// expanded contents of a group rollup.

import type { MetricCatalogEntry } from "../../api/types";
import {
  formatNumber,
  formatMetricValue,
  isByteUnit,
  isTimestampMetric,
  formatTimestampSeconds,
  formatTimestampSecondsCompact,
} from "../../lib/format";
import MetricTypePill from "./MetricTypePill";
import Sparkline from "./Sparkline";

// delta is computed from the sparkline's first vs last point so it lines
// up with what the row draws. Direction tiers at ±5%.
export function deltaOf(entry: MetricCatalogEntry): { pct: number; dir: "up" | "down" | "flat" } {
  const s = entry.spark ?? [];
  if (s.length < 2) return { pct: 0, dir: "flat" };
  const first = s[0];
  const last = s[s.length - 1];
  const pct = first !== 0 ? ((last - first) / Math.abs(first)) * 100 : 0;
  return { pct, dir: pct > 5 ? "up" : pct < -5 ? "down" : "flat" };
}

export default function MetricRow({
  entry,
  selected,
  onClick,
}: {
  entry: MetricCatalogEntry;
  selected: boolean;
  onClick: () => void;
}) {
  const breached = entry.threshold != null && entry.value > entry.threshold;
  const { pct, dir } = deltaOf(entry);
  const color = breached
    ? "var(--err)"
    : entry.severity === "warning" || entry.severity === "warn"
      ? "var(--warn)"
      : "var(--primary)";
  return (
    <div className={`mtbl-row ${selected ? "is-sel" : ""} ${breached ? "breached" : ""}`} onClick={onClick}>
      <div className="mt-name">
        <span className="m-name-text">{entry.name}</span>
        <span className="m-desc">
          {entry.unit ? `${entry.unit} · ` : ""}
          {entry.aggregation}
        </span>
      </div>
      <div>
        <MetricTypePill type={entry.type} />
      </div>
      <div className="mt-val">
        {isTimestampMetric(entry.unit, entry.name) ? (
          // Timestamp metric (e.g. file.mtime): show a compact "x ago" that
          // fits the narrow value cell; the full absolute date is in the
          // tooltip + the detail drawer. Unit ("s") / %-delta are dropped.
          <span className="m-val m-val--ts" title={formatTimestampSeconds(entry.value)}>
            {formatTimestampSecondsCompact(entry.value)}
          </span>
        ) : (
          <>
            <span className={`m-val ${breached ? "br" : ""}`}>{formatMetricValue(entry.value, entry.unit)}</span>
            {entry.unit && !isByteUnit(entry.unit) && <span className="m-unit">{entry.unit}</span>}
            <span className={`m-delta d-${dir}`}>
              {dir === "up" ? "▲" : dir === "down" ? "▼" : "–"} {pct >= 0 ? "+" : ""}
              {pct.toFixed(0)}%
            </span>
          </>
        )}
      </div>
      <div className="mt-spark">
        <Sparkline data={entry.spark} color={color} threshold={entry.threshold} />
      </div>
      <div className="mt-series">
        <span className="m-series-count">{formatNumber(entry.series_count)}</span>
        <span className="f-11">series</span>
      </div>
      <div className="mt-rules">
        {entry.rule_count > 0 ? (
          <span className={`m-rule-badge sev-${entry.severity || "warning"}`}>
            🔔 {entry.rule_count} rule{entry.rule_count > 1 ? "s" : ""}
          </span>
        ) : (
          <span className="f-11">—</span>
        )}
      </div>
    </div>
  );
}
