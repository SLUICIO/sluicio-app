// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The metric details drawer: header with the type pill + name + a 4-up
// stats block (Current / Avg / Peak / Series), the windowed chart, and
// the attribute filters carried from the search bar. The alert-rule
// builder section is supplied by the caller via `builder` (wired on the
// Metrics page) so this stays a presentational shell.

import type { ReactNode } from "react";
import type { LogAttrFilter, MetricCatalogEntry, Window } from "../../api/types";
import { isTimestampMetric, formatTimestampSeconds, formatMetricValue, isByteUnit } from "../../lib/format";
import MetricAttributes from "./MetricAttributes";
import MetricChart from "./MetricChart";
import MetricTypePill, { metricTypeLabel } from "./MetricTypePill";

const OP_GLYPH: Record<string, string> = {
  eq: "=", neq: "≠", contains: "contains", not_contains: "!contains",
  starts_with: "starts", exists: "exists", gt: ">", gte: "≥", lt: "<", lte: "≤",
};

export default function MetricsDrawer({
  entry,
  chips,
  window,
  range,
  breached,
  threshold,
  onClose,
  builder,
  onToggleFilter,
  isFilterActive,
}: {
  entry: MetricCatalogEntry;
  chips: LogAttrFilter[];
  window: Window;
  // Stable range string ("1h", or an absolute range the user pinned) for
  // the attribute fetches. We deliberately do NOT use window.from/to here:
  // for a relative range those shift on every catalog refetch, which would
  // collapse and reload the Attributes section each time a filter is added.
  range: string;
  breached?: boolean;
  threshold?: number | null;
  onClose: () => void;
  builder?: ReactNode;
  onToggleFilter?: (key: string, value: string) => void;
  isFilterActive?: (key: string, value: string) => boolean;
}) {
  const spark = entry.spark ?? [];
  const avg = spark.length ? spark.reduce((a, b) => a + b, 0) / spark.length : entry.value;
  const peak = spark.length ? Math.max(...spark) : entry.value;

  // Timestamp metrics (file.mtime, …) carry an epoch value: show it as a
  // date and drop the "s" unit. avg/peak of epochs aren't meaningful but at
  // least read as dates rather than raw numbers.
  const isTs = isTimestampMetric(entry.unit, entry.name);
  const stat = (k: string, v: number, br?: boolean) => (
    <div className="m-stat">
      <div className="m-stat-k">{k}</div>
      <div className={`m-stat-v ${br ? "br" : ""}`}>
        {isTs ? formatTimestampSeconds(v) : formatMetricValue(v, entry.unit)}
        {entry.unit && !isTs && !isByteUnit(entry.unit) && <span className="m-stat-u">{entry.unit}</span>}
      </div>
    </div>
  );

  return (
    <aside className="drawer m-drawer">
      <header className="drawer__head">
        <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
          <MetricTypePill type={entry.type} />
          <span style={{ flex: 1 }} />
          <button className="drawer__close" type="button" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>
        <div className="m-d-name">{entry.name}</div>
        <div className="m-d-desc">
          {metricTypeLabel(entry.type)} · {entry.aggregation}
          {entry.unit ? ` · ${entry.unit}` : ""}
        </div>
        <div className="m-d-stats">
          {stat("Current", entry.value, breached)}
          {stat("Window avg", avg)}
          {stat("Window peak", peak)}
          <div className="m-stat">
            <div className="m-stat-k">Series</div>
            <div className="m-stat-v">{entry.series_count}</div>
          </div>
        </div>
      </header>

      <div className="drawer__body m-d-body">
        <section>
          <div className="m-section-head">
            <span className="m-section-title">Windowed series</span>
            <span className="m-section-count">{spark.length} points</span>
          </div>
          <MetricChart
            data={spark}
            threshold={threshold}
            breached={breached}
            fromISO={window.from}
            toISO={window.to}
          />
        </section>

        <MetricAttributes
          metricName={entry.name}
          window={range}
          onToggleFilter={onToggleFilter}
          isActive={isFilterActive}
        />

        <section>
          <div className="m-section-head">
            <span className="m-section-title">Filters carried from search</span>
            <span className="m-section-count">{chips.length}</span>
          </div>
          <div className="m-carried-chips">
            {chips.length === 0 ? (
              <span className="muted" style={{ fontSize: 12 }}>
                No attribute filters applied — a rule would watch <span className="mono">any</span> series.
              </span>
            ) : (
              chips.map((c, i) => (
                <span key={i} className="fchip">
                  <span className="fchip__k">{c.key}</span>
                  <span className="fchip__o">{OP_GLYPH[c.op] ?? c.op}</span>
                  {c.op !== "exists" && <span className="fchip__v">{c.value}</span>}
                </span>
              ))
            )}
          </div>
        </section>

        {builder}
      </div>
    </aside>
  );
}
