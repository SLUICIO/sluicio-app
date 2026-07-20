// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// "Trim ingestion" — an advisory helper for keeping only the metrics you
// act on. Pick metrics to exclude and Sluicio generates an OpenTelemetry
// Collector filter processor that drops them upstream, before they reach
// Sluicio (saving egress + storage). Sluicio enforces nothing — you paste
// the config into your own collector. Each metric shows its rule count so
// you don't drop one you alert on by accident.

import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { api } from "../../api/client";
import type { MetricCatalogEntry } from "../../api/types";
import MetricAttributesInline from "./MetricAttributesInline";

// AttrRule drops only the DATAPOINTS of a metric where one attribute
// carries one value — the scalpel next to the whole-metric axe. A
// health-check route or one noisy tenant often dominates a metric the
// rest of which you want to keep.
export interface AttrRule {
  metric: string;
  key: string;
  value: string;
}

// How many metric rows to render at first, and to add each time the user
// scrolls near the bottom. The full catalog is fetched once (the filter +
// prefix suggestions need it), but only this many rows are mounted at a
// time so a multi-thousand-metric catalog stays responsive.
const RENDER_PAGE = 120;

// CodeMirror is a heavy chunk — load it only when this modal opens.
const CodeEditor = lazy(() => import("../CodeEditor"));

// escapeRe escapes regex metacharacters so a literal prefix (which may
// contain "." or "-") becomes a safe OTTL IsMatch anchor.
function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

// PrefixSuggestion: a separator-bounded prefix shared by `count` of the
// currently-selected metrics, collapsible into one IsMatch rule.
interface PrefixSuggestion {
  prefix: string;
  count: number;
}

// suggestPrefixes finds common separator-bounded prefixes among the
// selected metric names that aren't already covered by an active prefix
// rule. Used to offer "drop everything starting with abc_" instead of N
// individual lines. Keeps only the broadest grouping per family.
function suggestPrefixes(names: string[], active: string[]): PrefixSuggestion[] {
  const counts = new Map<string, number>();
  for (const nm of names) {
    for (let i = 1; i < nm.length - 1; i++) {
      if (/[._:-]/.test(nm[i])) {
        const p = nm.slice(0, i + 1);
        counts.set(p, (counts.get(p) ?? 0) + 1);
      }
    }
  }
  let cands = [...counts.entries()]
    .filter(([p, c]) => c >= 3 && p.length >= 3)
    .filter(([p]) => !active.some((ap) => p.startsWith(ap)))
    .map(([prefix, count]) => ({ prefix, count }));
  // Prefer the most specific (longest) prefix that still covers the family:
  // drop a candidate if a LONGER prefix extending it covers at least as many
  // (e.g. keep "rabbitmq.node." over "rabbitmq." when both cover the same 3
  // selected metrics). Only when a longer prefix covers fewer do we fall back
  // to the shorter, broader one.
  cands = cands.filter(
    (c) => !cands.some((o) => o !== c && o.prefix.startsWith(c.prefix) && o.count >= c.count),
  );
  cands.sort((a, b) => b.count - a.count || b.prefix.length - a.prefix.length);
  return cands.slice(0, 3);
}

// buildCollectorConfig renders the OTel filter processor: one
// IsMatch(name, "^prefix") condition per active prefix rule, plus an
// exact name== condition for every selected metric not covered by a
// prefix. Conditions that match are dropped before reaching Sluicio.
function buildCollectorConfig(names: string[], prefixes: string[], attrRules: AttrRule[]): string {
  const pfx = [...prefixes].sort();
  const covered = (nm: string) => pfx.some((p) => nm.startsWith(p));
  const exact = names.filter((nm) => !covered(nm)).sort();
  const esc = (s: string) => s.replace(/"/g, '\\"');
  // Attribute rules for a metric that's ALREADY dropped wholesale are
  // redundant — skip them so the generated config says what it means.
  const whole = new Set(names);
  const dp = attrRules
    .filter((r) => !whole.has(r.metric) && !covered(r.metric))
    .sort((a, b) => a.metric.localeCompare(b.metric) || a.key.localeCompare(b.key) || a.value.localeCompare(b.value));
  if (pfx.length === 0 && exact.length === 0 && dp.length === 0) {
    return "# Select metrics on the left to generate a collector config…";
  }
  const metricLines: string[] = [];
  for (const p of pfx) metricLines.push(`        - 'IsMatch(name, "^${escapeRe(p)}")'`);
  for (const nm of exact) metricLines.push(`        - 'name == "${esc(nm)}"'`);
  const dpLines = dp.map(
    (r) => `        - 'metric.name == "${esc(r.metric)}" and attributes["${esc(r.key)}"] == "${esc(r.value)}"'`,
  );
  const sections: string[] = [];
  if (metricLines.length > 0) sections.push(`      metric:\n${metricLines.join("\n")}`);
  if (dpLines.length > 0)
    sections.push(`      # datapoint conditions drop only the matching series — the rest
      # of the metric keeps flowing
      datapoint:\n${dpLines.join("\n")}`);
  return `# Drop these metrics at your OpenTelemetry Collector, before they
# reach Sluicio. Conditions that match are dropped.
processors:
  filter/sluicio-exclude:
    error_mode: ignore
    metrics:
${sections.join("\n")}

service:
  pipelines:
    metrics:
      # add the processor alongside your existing ones, before the
      # exporter that sends to Sluicio
      processors: [filter/sluicio-exclude]`;
}

export default function TrimIngestionPanel({ window: win, onClose }: { window: string; onClose: () => void }) {
  const [all, setAll] = useState<MetricCatalogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [filter, setFilter] = useState("");
  const [excluded, setExcluded] = useState<Set<string>>(new Set());
  const [prefixes, setPrefixes] = useState<Set<string>>(new Set());
  const [attrRules, setAttrRules] = useState<AttrRule[]>([]);
  // Which metric row's attribute picker is expanded (one at a time).
  const [attrOpenFor, setAttrOpenFor] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  // Full screen + draggable split between the metric picker and the
  // generated config; leftPct is the picker's share of the width.
  const [fullscreen, setFullscreen] = useState(false);
  const [leftPct, setLeftPct] = useState(50);
  // How many filtered rows are currently mounted (grows on scroll).
  const [renderCount, setRenderCount] = useState(RENDER_PAGE);
  const gridRef = useRef<HTMLDivElement>(null);
  const draggingRef = useRef(false);

  // Column resize: while the divider is held, map the cursor X to a
  // percentage of the grid width (clamped so neither pane collapses).
  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!draggingRef.current || !gridRef.current) return;
      const rect = gridRef.current.getBoundingClientRect();
      const pct = ((e.clientX - rect.left) / rect.width) * 100;
      setLeftPct(Math.min(80, Math.max(20, pct)));
      e.preventDefault();
    };
    const onUp = () => {
      if (!draggingRef.current) return;
      draggingRef.current = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
    return () => {
      document.removeEventListener("mousemove", onMove);
      document.removeEventListener("mouseup", onUp);
    };
  }, []);

  useEffect(() => {
    setLoading(true);
    api
      .metricCatalog(win, {})
      .then((r) => setAll(r.metrics ?? []))
      .catch(() => setAll([]))
      .finally(() => setLoading(false));
  }, [win]);

  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("keydown", h);
    return () => document.removeEventListener("keydown", h);
  }, [onClose]);

  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return q ? all.filter((m) => m.name.toLowerCase().includes(q)) : all;
  }, [all, filter]);

  // Reset the render window whenever the visible set changes (new filter,
  // catalog loaded) so we start from the top, then grow on scroll.
  useEffect(() => setRenderCount(RENDER_PAGE), [filter, all]);
  const shown = useMemo(() => visible.slice(0, renderCount), [visible, renderCount]);

  const onListScroll = (e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 240) {
      setRenderCount((c) => (c < visible.length ? c + RENDER_PAGE : c));
    }
  };

  const yaml = useMemo(
    () => buildCollectorConfig([...excluded].sort(), [...prefixes], attrRules),
    [excluded, prefixes, attrRules],
  );
  const suggestions = useMemo(() => suggestPrefixes([...excluded], [...prefixes]), [excluded, prefixes]);
  const prefixList = useMemo(() => [...prefixes].sort(), [prefixes]);
  const coveredByPrefix = (name: string) => prefixList.some((p) => name.startsWith(p));

  const toggle = (name: string) =>
    setExcluded((prev) => {
      const n = new Set(prev);
      n.has(name) ? n.delete(name) : n.add(name);
      return n;
    });

  const addPrefix = (p: string) => setPrefixes((prev) => new Set(prev).add(p));
  const removePrefix = (p: string) =>
    setPrefixes((prev) => {
      const n = new Set(prev);
      n.delete(p);
      return n;
    });
  const clearAll = () => {
    setExcluded(new Set());
    setPrefixes(new Set());
    setAttrRules([]);
    setAttrOpenFor(null);
  };

  const addAttrRule = (metric: string, key: string, value: string) =>
    setAttrRules((prev) =>
      prev.some((r) => r.metric === metric && r.key === key && r.value === value)
        ? prev
        : [...prev, { metric, key, value }],
    );
  const removeAttrRule = (rule: AttrRule) =>
    setAttrRules((prev) =>
      prev.filter((r) => !(r.metric === rule.metric && r.key === rule.key && r.value === rule.value)),
    );

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(yaml);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard blocked — the config is visible to copy manually */
    }
  };

  return (
    <div
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(15,23,42,0.45)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 200,
        padding: 24,
      }}
    >
      <div
        className="card"
        onClick={(e) => e.stopPropagation()}
        style={
          fullscreen
            ? { width: "98vw", height: "96vh", maxHeight: "96vh", display: "flex", flexDirection: "column" }
            : { width: "min(880px, 94vw)", maxHeight: "88vh", display: "flex", flexDirection: "column" }
        }
      >
        <div className="card__header" style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <span>Trim ingestion · exclude metrics from Sluicio</span>
          <div style={{ display: "flex", gap: 4, alignItems: "center" }}>
            <button
              className="drawer__close"
              type="button"
              onClick={() => setFullscreen((f) => !f)}
              aria-label={fullscreen ? "Exit full screen" : "Full screen"}
              title={fullscreen ? "Exit full screen" : "Full screen"}
            >
              {fullscreen ? "🗗" : "⛶"}
            </button>
            <button className="drawer__close" type="button" onClick={onClose} aria-label="Close">✕</button>
          </div>
        </div>

        <div
          ref={gridRef}
          style={{
            padding: "12px 16px",
            display: "grid",
            gridTemplateColumns: `${leftPct}% 8px minmax(0, 1fr)`,
            gap: 8,
            minHeight: 0,
            flex: 1,
            overflow: "hidden",
          }}
        >
          {/* metric picker */}
          <div style={{ display: "flex", flexDirection: "column", minHeight: 0 }}>
            <input
              className="search__input"
              placeholder="Filter metrics…"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              // .search__input has flex:1 (for horizontal toolbars); in this
              // vertical column that would stretch it to fill the modal
              // height, so pin it to its natural row height.
              style={{ flex: "none" }}
            />
            <div style={{ display: "flex", gap: 8, alignItems: "center", margin: "8px 0", fontSize: 12 }}>
              <button
                className="btn btn--sm"
                type="button"
                onClick={() => setExcluded((p) => new Set([...p, ...visible.map((m) => m.name)]))}
              >
                Exclude shown
              </button>
              <button className="btn btn--sm" type="button" onClick={clearAll}>
                Clear
              </button>
              <span className="muted" style={{ marginLeft: "auto" }}>
                {excluded.size} excluded{prefixes.size > 0 ? ` · ${prefixes.size} prefix rule${prefixes.size > 1 ? "s" : ""}` : ""}
                {attrRules.length > 0 ? ` · ${attrRules.length} attribute rule${attrRules.length > 1 ? "s" : ""}` : ""}
              </span>
            </div>

            {(prefixList.length > 0 || suggestions.length > 0) && (
              <div style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center", margin: "0 0 8px", fontSize: 12 }}>
                {prefixList.map((p) => (
                  <span key={p} className="m-rule-badge" style={{ display: "inline-flex", alignItems: "center", gap: 4, padding: "2px 4px 2px 8px" }} title={`Drops every metric matching ^${p}`}>
                    <span className="mono">{p}*</span>
                    <button type="button" aria-label={`Remove prefix rule ${p}`} onClick={() => removePrefix(p)}
                      style={{ border: 0, background: "transparent", cursor: "pointer", color: "inherit", fontSize: 13, lineHeight: 1, padding: "0 2px" }}>×</button>
                  </span>
                ))}
                {suggestions.map((s) => (
                  <button key={s.prefix} className="btn btn--sm" type="button"
                    title={`Collapse the ${s.count} selected metrics starting with "${s.prefix}" into one IsMatch rule`}
                    onClick={() => addPrefix(s.prefix)}>
                    + <span className="mono">{s.prefix}*</span> ({s.count})
                  </button>
                ))}
              </div>
            )}
            {attrRules.length > 0 && (
              <div style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center", margin: "0 0 8px", fontSize: 12 }}>
                {attrRules.map((r) => (
                  <span
                    key={`${r.metric}|${r.key}|${r.value}`}
                    className="m-rule-badge"
                    style={{ display: "inline-flex", alignItems: "center", gap: 4, padding: "2px 4px 2px 8px" }}
                    title={`Drops only the datapoints of ${r.metric} where ${r.key} = ${r.value}`}
                  >
                    <span className="mono">{r.metric}{"{"}{r.key}={r.value}{"}"}</span>
                    <button type="button" aria-label={`Remove attribute rule ${r.metric} ${r.key}=${r.value}`} onClick={() => removeAttrRule(r)}
                      style={{ border: 0, background: "transparent", cursor: "pointer", color: "inherit", fontSize: 13, lineHeight: 1, padding: "0 2px" }}>×</button>
                  </span>
                ))}
              </div>
            )}
            <div onScroll={onListScroll} style={{ overflow: "auto", border: "1px solid var(--border)", borderRadius: 8, minHeight: 0, flex: 1 }}>
              {loading ? (
                <div className="placeholder" style={{ margin: 10 }}>Loading metrics…</div>
              ) : visible.length === 0 ? (
                <div className="placeholder" style={{ margin: 10 }}>No metrics match.</div>
              ) : (
                shown.map((m) => (
                  <div key={m.name} style={{ borderBottom: "1px solid var(--border)" }}>
                    <label
                      style={{
                        display: "flex",
                        gap: 8,
                        alignItems: "center",
                        padding: "6px 10px",
                        cursor: "pointer",
                      }}
                    >
                      <input type="checkbox" checked={excluded.has(m.name)} onChange={() => toggle(m.name)} />
                      <span className="mono" style={{ fontSize: 12.5, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                        {m.name}
                      </span>
                      <span style={{ marginLeft: "auto", fontSize: 11, whiteSpace: "nowrap", display: "inline-flex", alignItems: "center" }}>
                        <span className="muted">{m.series_count} series</span>
                        {coveredByPrefix(m.name) && (
                          <span className="m-rule-badge" style={{ marginLeft: 6, padding: "1px 6px" }} title="Dropped by a prefix rule">
                            prefix
                          </span>
                        )}
                        {m.rule_count > 0 && (
                          <span className="m-rule-badge" style={{ marginLeft: 6, padding: "1px 6px" }} title="Watched by an alert rule">
                            🔔 {m.rule_count}
                          </span>
                        )}
                        {attrRules.some((r) => r.metric === m.name) && (
                          <span className="m-rule-badge" style={{ marginLeft: 6, padding: "1px 6px" }} title="Has attribute-scoped drop rules">
                            {attrRules.filter((r) => r.metric === m.name).length} attr
                          </span>
                        )}
                        <button
                          type="button"
                          className="btn btn--link"
                          style={{ marginLeft: 6, padding: 0, fontSize: 11 }}
                          title="Trim by attribute — drop only the datapoints carrying a specific attribute value"
                          onClick={(e) => {
                            e.preventDefault();
                            setAttrOpenFor((cur) => (cur === m.name ? null : m.name));
                          }}
                        >
                          {attrOpenFor === m.name ? "attrs ▾" : "attrs ▸"}
                        </button>
                      </span>
                    </label>
                    {attrOpenFor === m.name && (
                      <div style={{ padding: "0 10px 8px 30px" }}>
                        <MetricAttributesInline
                          metric={m.name}
                          window={win}
                          onPickValue={(key, value) => addAttrRule(m.name, key, value)}
                        />
                      </div>
                    )}
                  </div>
                ))
              )}
              {!loading && shown.length < visible.length && (
                <div className="placeholder" style={{ margin: 10, textAlign: "center", fontSize: 12 }}>
                  Showing {shown.length} of {visible.length} — scroll for more
                </div>
              )}
            </div>
          </div>

          {/* draggable column divider */}
          <div
            onMouseDown={() => {
              draggingRef.current = true;
              document.body.style.cursor = "col-resize";
              document.body.style.userSelect = "none";
            }}
            onDoubleClick={() => setLeftPct(50)}
            title="Drag to resize · double-click to reset"
            style={{
              cursor: "col-resize",
              alignSelf: "stretch",
              width: 8,
              borderRadius: 4,
              background: "var(--border)",
            }}
          />

          {/* generated config */}
          <div style={{ display: "flex", flexDirection: "column", minHeight: 0 }}>
            <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 8 }}>
              <span className="m-field-label">OTel Collector config</span>
              <button className="btn btn--sm btn--primary" type="button" onClick={copy} disabled={excluded.size === 0 && prefixes.size === 0 && attrRules.length === 0}>
                {copied ? "Copied ✓" : "Copy"}
              </button>
            </div>
            {/* Read-only CodeMirror: YAML highlighting + line numbers, and
                line wrapping so long metric names wrap onto the next row
                instead of overflowing the modal. */}
            <div style={{ flex: 1, minHeight: 0, display: "flex", flexDirection: "column" }}>
              <Suspense
                fallback={<pre className="drawer__raw" style={{ flex: 1, margin: 0 }}>{yaml}</pre>}
              >
                <CodeEditor value={yaml} onChange={() => {}} format="yaml" readOnly height="100%" />
              </Suspense>
            </div>
          </div>
        </div>

        <div style={{ padding: "10px 16px", borderTop: "1px solid var(--border)", fontSize: 12, color: "var(--muted)" }}>
          Sluicio doesn't enforce this — paste the processor into your OpenTelemetry Collector's metrics pipeline to stop
          these series being sent. Metrics watched by an alert rule are flagged 🔔 so you don't drop one by accident.
          Use <b>attrs</b> on a row to drop only the datapoints carrying a specific attribute value (a health-check
          route, one noisy tenant) while the rest of the metric keeps flowing.
        </div>
      </div>
    </div>
  );
}
