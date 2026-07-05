// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The per-metric "Attributes" section in the drawer: the attribute keys
// present on this metric's series (service.name, k8s.pod.name,
// messaging.system, …) grouped Resource vs Metric, each expandable to
// its top values. Backed by /metric-fields?metric= and
// /metric-attributes/{key}/values?metric=.
//
// Each value is clickable: it toggles an `attr = value` filter on the
// explorer so you can drill from "all queues" to one queue, and that
// scope carries into the metric value, the chart, and the health-check
// builder. Values are capped to the top 10 per attribute; a per-attribute
// search box hits the backend so a value buried beyond the cap (an
// attribute may have hundreds) is still findable.

import { useEffect, useMemo, useState } from "react";
import { api } from "../../api/client";
import type { LogAttrValue, LogFieldEntry } from "../../api/types";

const RESOURCE_PREFIXES = [
  "service.", "k8s.", "host.", "cloud.", "deployment.",
  "telemetry.", "process.", "container.", "os.",
];
const isResource = (k: string) => RESOURCE_PREFIXES.some((p) => k.startsWith(p));

const VALUE_CAP = 10;

function formatCount(n: number): string {
  if (n >= 1000) return `${(n / 1000).toFixed(1)}k`;
  return String(n);
}

export default function MetricAttributes({
  metricName,
  window,
  onToggleFilter,
  isActive,
}: {
  metricName: string;
  window: string;
  // Toggle an `key = value` eq filter on the explorer. When omitted the
  // value list is read-only.
  onToggleFilter?: (key: string, value: string) => void;
  isActive?: (key: string, value: string) => boolean;
}) {
  const [keys, setKeys] = useState<LogFieldEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [open, setOpen] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [vals, setVals] = useState<LogAttrValue[]>([]);
  const [valsLoading, setValsLoading] = useState(false);

  useEffect(() => {
    setLoading(true);
    setOpen(null);
    setVals([]);
    api
      .metricFields(window, metricName)
      .then((r) => setKeys(r.fields ?? []))
      .catch(() => setKeys([]))
      .finally(() => setLoading(false));
  }, [metricName, window]);

  // Fetch the open attribute's values, debounced on the search box so
  // typing queries the full cardinality server-side rather than only the
  // first page of values.
  useEffect(() => {
    if (!open) {
      setVals([]);
      return;
    }
    setValsLoading(true);
    const key = open;
    const q = search.trim();
    const handle = setTimeout(() => {
      api
        .metricAttributeValues(key, window, VALUE_CAP, metricName, q || undefined)
        .then((r) => setVals(r.values ?? []))
        .catch(() => setVals([]))
        .finally(() => setValsLoading(false));
    }, q ? 250 : 0);
    return () => clearTimeout(handle);
  }, [open, search, metricName, window]);

  const groups = useMemo(() => {
    const resource = keys.filter((k) => isResource(k.key));
    const metric = keys.filter((k) => !isResource(k.key));
    const out: { label: string; items: LogFieldEntry[] }[] = [];
    if (metric.length) out.push({ label: "Metric", items: metric });
    if (resource.length) out.push({ label: "Resource", items: resource });
    return out;
  }, [keys]);

  const toggle = (key: string) => {
    if (open === key) {
      setOpen(null);
      setSearch("");
      return;
    }
    setOpen(key);
    setSearch("");
  };

  const clickable = !!onToggleFilter;

  return (
    <section>
      <div className="m-section-head">
        <span className="m-section-title">Attributes</span>
        <span className="m-section-count">{keys.length}</span>
      </div>
      {loading ? (
        <div className="placeholder" style={{ margin: 0 }}>Loading attributes…</div>
      ) : keys.length === 0 ? (
        <div className="placeholder" style={{ margin: 0 }}>No attributes on this metric in the window.</div>
      ) : (
        groups.map((g) => (
          <div key={g.label}>
            <div className="m-attr__group">{g.label}</div>
            <div className="m-attrs">
              {g.items.map((f) => {
                const isOpen = open === f.key;
                const overflow = f.cardinality > VALUE_CAP;
                return (
                  <div className="m-attr" key={f.key}>
                    <button type="button" className="m-attr__head" onClick={() => toggle(f.key)}>
                      <span className="m-attr__key">{f.key}</span>
                      <span className={`typechip typechip--${f.type === "number" ? "number" : "string"}`}>{f.type}</span>
                      <span className="m-attr__count">
                        {formatCount(f.cardinality)} {f.cardinality === 1 ? "value" : "values"} {isOpen ? "▾" : "▸"}
                      </span>
                    </button>
                    {isOpen && (
                      <div className="m-attr__vals">
                        {overflow && (
                          <input
                            className="search__input mono"
                            style={{ fontSize: 12, padding: "4px 8px", marginBottom: 4 }}
                            placeholder={`Search ${formatCount(f.cardinality)} values…`}
                            value={search}
                            autoFocus
                            onChange={(e) => setSearch(e.target.value)}
                          />
                        )}
                        {valsLoading ? (
                          <span className="muted" style={{ fontSize: 12 }}>Loading…</span>
                        ) : vals.length === 0 ? (
                          <span className="muted" style={{ fontSize: 12 }}>
                            {search.trim() ? "No matching values." : "No values."}
                          </span>
                        ) : (
                          <>
                            {vals.map((v) => {
                              const on = isActive?.(f.key, v.value) ?? false;
                              const row = (
                                <>
                                  <span className="v" title={v.value}>
                                    {clickable && <span style={{ marginRight: 6, opacity: on ? 1 : 0.35 }}>{on ? "✓" : "+"}</span>}
                                    {v.value}
                                  </span>
                                  <span className="n">{formatCount(v.events)}</span>
                                </>
                              );
                              return clickable ? (
                                <button
                                  type="button"
                                  className={`m-attr__val m-attr__val--btn${on ? " is-on" : ""}`}
                                  key={v.value}
                                  title={on ? `Remove filter ${f.key} = ${v.value}` : `Filter ${f.key} = ${v.value}`}
                                  onClick={() => onToggleFilter!(f.key, v.value)}
                                >
                                  {row}
                                </button>
                              ) : (
                                <div className="m-attr__val" key={v.value}>{row}</div>
                              );
                            })}
                            {overflow && !search.trim() && (
                              <span className="muted" style={{ fontSize: 11 }}>
                                Top {VALUE_CAP} of {formatCount(f.cardinality)} — search to find the rest.
                              </span>
                            )}
                            {search.trim() && vals.length === VALUE_CAP && (
                              <span className="muted" style={{ fontSize: 11 }}>
                                First {VALUE_CAP} matches — refine to narrow.
                              </span>
                            )}
                          </>
                        )}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        ))
      )}
    </section>
  );
}
