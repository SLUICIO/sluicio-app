// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// MetricAttributesInline — the attribute breakdown of one metric: its
// attribute keys (use count · cardinality) expanding to the top values
// with datapoint counts. Used read-only in the metrics report, and with
// an onPickValue callback inside the Trim-ingestion panel, where
// clicking a value adds an attribute-scoped drop rule.

import { useEffect, useState } from "react";
import { api } from "../../api/client";
import type { LogAttrValue, LogFieldEntry } from "../../api/types";

export default function MetricAttributesInline({
  metric,
  window: win,
  onPickValue,
}: {
  metric: string;
  window: string;
  // When set, values render as buttons and picking one calls back —
  // the trim panel turns it into a datapoint drop rule.
  onPickValue?: (key: string, value: string) => void;
}) {
  const [fields, setFields] = useState<LogFieldEntry[] | null>(null);
  const [openKey, setOpenKey] = useState<string | null>(null);
  const [values, setValues] = useState<Record<string, LogAttrValue[] | null>>({});

  useEffect(() => {
    let alive = true;
    setFields(null);
    api
      .metricFields(win, metric)
      .then((r) => {
        if (alive) setFields(r.fields ?? []);
      })
      .catch(() => {
        if (alive) setFields([]);
      });
    return () => {
      alive = false;
    };
  }, [metric, win]);

  const toggleKey = (k: string) => {
    setOpenKey((cur) => (cur === k ? null : k));
    if (values[k] === undefined) {
      setValues((v) => ({ ...v, [k]: null }));
      api
        .metricAttributeValues(k, win, 20, metric)
        .then((r) => setValues((v) => ({ ...v, [k]: r.values ?? [] })))
        .catch(() => setValues((v) => ({ ...v, [k]: [] })));
    }
  };

  if (fields === null) return <div className="muted" style={{ fontSize: 12, padding: "4px 0" }}>Loading attributes…</div>;
  if (fields.length === 0)
    return <div className="muted" style={{ fontSize: 12, padding: "4px 0" }}>No datapoint attributes seen on this metric in the window.</div>;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 2, fontSize: 12 }}>
      {fields.map((f) => (
        <div key={f.key}>
          <button
            type="button"
            onClick={() => toggleKey(f.key)}
            className="btn btn--link"
            style={{ padding: "2px 0", display: "inline-flex", gap: 8, alignItems: "baseline" }}
            title="Show the top values for this attribute"
          >
            <span>{openKey === f.key ? "▾" : "▸"}</span>
            <span className="mono">{f.key}</span>
            <span className="muted">
              {f.use_count.toLocaleString()} points · {f.cardinality.toLocaleString()} value{f.cardinality === 1 ? "" : "s"}
            </span>
          </button>
          {openKey === f.key && (
            <div style={{ marginLeft: 22, display: "flex", flexDirection: "column", gap: 1 }}>
              {values[f.key] === null && <span className="muted">Loading values…</span>}
              {(values[f.key] ?? []).map((v) => (
                <span key={v.value} style={{ display: "inline-flex", gap: 8, alignItems: "baseline" }}>
                  {onPickValue ? (
                    <button
                      type="button"
                      className="btn btn--link mono"
                      style={{ padding: 0, fontSize: 12 }}
                      title={`Drop datapoints where ${f.key} = ${v.value}`}
                      onClick={() => onPickValue(f.key, v.value)}
                    >
                      ✂ {v.value}
                    </button>
                  ) : (
                    <span className="mono">{v.value}</span>
                  )}
                  <span className="muted">{v.events.toLocaleString()} points</span>
                </span>
              ))}
              {values[f.key]?.length === 0 && <span className="muted">no values in the window</span>}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
