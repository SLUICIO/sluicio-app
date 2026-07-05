// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Value tiles for a service's "show on service page" health checks.
// Custom metrics are unified into health checks: any check with
// display_on_service set surfaces its latest reading here — the value
// the evaluator computed (telemetry) or the last value pushed in
// (pushed). A breaching reading is highlighted.

import { useCallback, useEffect, useState } from "react";
import { api } from "../../api/client";
import type { ServiceReading } from "../../api/types";
import { formatMetricValue, isByteUnit, formatRelative, isSecondsUnit, formatDurationSeconds } from "../../lib/format";

const OP_GLYPH: Record<string, string> = {
  gt: ">",
  gte: "≥",
  lt: "<",
  lte: "≤",
  eq: "=",
  neq: "≠",
};

export default function ServiceReadingTiles({ serviceName }: { serviceName: string }) {
  const [readings, setReadings] = useState<ServiceReading[]>([]);
  const [loaded, setLoaded] = useState(false);

  const refresh = useCallback(() => {
    api
      .serviceReadings(serviceName)
      .then((r) => setReadings(r.readings ?? []))
      .catch(() => setReadings([]))
      .finally(() => setLoaded(true));
  }, [serviceName]);

  useEffect(() => {
    refresh();
  }, [refresh]);

  // Nothing flagged for display → render nothing (keeps the page clean
  // for services with no value-tile checks).
  if (!loaded || readings.length === 0) return null;

  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="card__header">Current values</div>
      <div
        style={{
          padding: "12px 16px",
          display: "grid",
          gridTemplateColumns: "repeat(auto-fill, minmax(180px, 1fr))",
          gap: 12,
        }}
      >
        {readings.map((rd) => (
          <div
            key={rd.rule_id}
            className="card"
            style={{
              margin: 0,
              padding: 12,
              borderColor: rd.breached ? "var(--err)" : undefined,
            }}
          >
            <div className="muted" style={{ fontSize: 12, marginBottom: 4 }}>
              {rd.name}
              {rd.source === "pushed" && (
                <span className="m-rule-badge" style={{ marginLeft: 6 }}>pushed</span>
              )}
            </div>
            {rd.has_value ? (
              <div
                style={{
                  fontSize: 22,
                  fontWeight: 600,
                  color: rd.breached ? "var(--err-ink)" : "var(--ink)",
                }}
              >
                {/* Seconds-unit readings (e.g. an "age" staleness check)
                    read better as a duration than a raw second count. */}
                {isSecondsUnit(rd.unit) ? (
                  formatDurationSeconds(rd.value ?? 0)
                ) : (
                  <>
                    {formatMetricValue(rd.value ?? 0, rd.unit)}
                    {rd.unit && !isByteUnit(rd.unit) ? <span style={{ fontSize: 13, fontWeight: 400 }}> {rd.unit}</span> : null}
                  </>
                )}
              </div>
            ) : (
              <div className="muted" style={{ fontSize: 14 }}>No value yet</div>
            )}
            <div className="muted" style={{ fontSize: 11.5, marginTop: 4 }}>
              threshold {OP_GLYPH[rd.operator] ?? rd.operator}{" "}
              {isSecondsUnit(rd.unit)
                ? formatDurationSeconds(rd.threshold)
                : `${formatMetricValue(rd.threshold, rd.unit)}${rd.unit && !isByteUnit(rd.unit) ? ` ${rd.unit}` : ""}`}
              {rd.observed_at ? ` · ${formatRelative(rd.observed_at)}` : ""}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
