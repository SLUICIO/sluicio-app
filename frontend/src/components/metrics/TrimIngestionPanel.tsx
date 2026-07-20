// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// "Trim ingestion" — an advisory helper for keeping only the telemetry
// you act on. Pick metrics, log services, or trace services to exclude
// and Sluicio generates an OpenTelemetry Collector config that drops
// them upstream, before they reach Sluicio (saving egress + storage).
// Sluicio enforces nothing — you paste the config into your own
// collector. Everything an alert rule watches is flagged so you don't
// drop what you monitor by accident.

import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { api } from "../../api/client";
import type { MetricCatalogEntry, UsageReportResponse, UsageServiceRow } from "../../api/types";
import { formatBytes } from "../../lib/format";
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

// LogTrimRule drops a service's log records — all of them, or only those
// below a severity floor (the common real-world trim: shed debug/info
// noise, keep warnings and errors so alerting stays possible).
export interface LogTrimRule {
  service: string;
  floor: "all" | "warn" | "error";
}

// TraceTrimRule handles a service's spans: "drop" filters them out
// entirely; "sample" keeps a share via tail sampling — the honest cost
// lever when the service still feeds integration health.
export interface TraceTrimRule {
  service: string;
  mode: "drop" | "sample";
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

// buildCollectorConfig renders the OTel Collector processors: a filter
// processor with per-signal sections (metric name/prefix + datapoint
// conditions, per-service log_record conditions with optional severity
// floors, per-service span drops) and — when trace sampling is chosen —
// a tail_sampling processor that keeps a share of the picked services'
// traces while passing everything else through untouched.
function buildCollectorConfig(
  names: string[],
  prefixes: string[],
  attrRules: AttrRule[],
  logRules: LogTrimRule[],
  traceRules: TraceTrimRule[],
  samplePct: number,
): string {
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
  const logs = [...logRules].sort((a, b) => a.service.localeCompare(b.service));
  const spanDrops = traceRules.filter((r) => r.mode === "drop").sort((a, b) => a.service.localeCompare(b.service));
  const sampled = traceRules.filter((r) => r.mode === "sample").sort((a, b) => a.service.localeCompare(b.service));
  if (pfx.length === 0 && exact.length === 0 && dp.length === 0 && logs.length === 0 && spanDrops.length === 0 && sampled.length === 0) {
    return "# Select metrics, log services, or trace services on the left\n# to generate a collector config…";
  }

  const metricLines: string[] = [];
  for (const p of pfx) metricLines.push(`        - 'IsMatch(name, "^${escapeRe(p)}")'`);
  for (const nm of exact) metricLines.push(`        - 'name == "${esc(nm)}"'`);
  const dpLines = dp.map(
    (r) => `        - 'metric.name == "${esc(r.metric)}" and attributes["${esc(r.key)}"] == "${esc(r.value)}"'`,
  );
  const metricSections: string[] = [];
  if (metricLines.length > 0) metricSections.push(`      metric:\n${metricLines.join("\n")}`);
  if (dpLines.length > 0)
    metricSections.push(`      # datapoint conditions drop only the matching series — the rest
      # of the metric keeps flowing
      datapoint:\n${dpLines.join("\n")}`);

  // Per-signal filter sections.
  const signalSections: string[] = [];
  if (metricSections.length > 0) signalSections.push(`    metrics:\n${metricSections.join("\n")}`);
  if (logs.length > 0) {
    const lines = logs.map((r) => {
      const svc = `resource.attributes["service.name"] == "${esc(r.service)}"`;
      if (r.floor === "warn") return `        - '${svc} and severity_number < SEVERITY_NUMBER_WARN'`;
      if (r.floor === "error") return `        - '${svc} and severity_number < SEVERITY_NUMBER_ERROR'`;
      return `        - '${svc}'`;
    });
    signalSections.push(`    logs:
      # severity floors keep warnings/errors flowing so you can still
      # alert on them later — only the noise below is dropped
      log_record:\n${lines.join("\n")}`);
  }
  if (spanDrops.length > 0) {
    const lines = spanDrops.map((r) => `        - 'resource.attributes["service.name"] == "${esc(r.service)}"'`);
    signalSections.push(`    traces:
      # CAUTION: dropping a service's spans removes it from Sluicio's
      # integration health entirely — prefer sampling if it's monitored
      span:\n${lines.join("\n")}`);
  }

  const blocks: string[] = [];
  if (signalSections.length > 0) {
    blocks.push(`  filter/sluicio-exclude:
    error_mode: ignore
${signalSections.join("\n")}`);
  }
  if (sampled.length > 0) {
    const values = sampled.map((r) => esc(r.service)).join(", ");
    blocks.push(`  # Keep ${samplePct}% of these services' traces; every other
  # service passes through untouched.
  tail_sampling/sluicio-trim:
    policies:
      - name: sample-noisy-services
        type: and
        and:
          and_sub_policy:
            - name: the-services
              type: string_attribute
              string_attribute: {key: service.name, values: [${values}]}
            - name: keep-a-share
              type: probabilistic
              probabilistic: {sampling_percentage: ${samplePct}}
      - name: keep-everything-else
        type: and
        and:
          and_sub_policy:
            - name: all-other-services
              type: string_attribute
              string_attribute: {key: service.name, values: [${values}], invert_match: true}
            - name: keep
              type: always_sample`);
  }

  // Pipeline wiring: only the pipelines that gained a processor.
  const pipelines: string[] = [];
  if (metricSections.length > 0) pipelines.push(`    metrics:
      processors: [filter/sluicio-exclude]`);
  if (logs.length > 0) pipelines.push(`    logs:
      processors: [filter/sluicio-exclude]`);
  if (spanDrops.length > 0 || sampled.length > 0) {
    const procs = [
      ...(spanDrops.length > 0 ? ["filter/sluicio-exclude"] : []),
      ...(sampled.length > 0 ? ["tail_sampling/sluicio-trim"] : []),
    ];
    pipelines.push(`    traces:
      processors: [${procs.join(", ")}]`);
  }

  return `# Drop this telemetry at your OpenTelemetry Collector, before it
# reaches Sluicio. Conditions that match are dropped.
processors:
${blocks.join("\n")}

service:
  pipelines:
    # add each processor alongside your existing ones, before the
    # exporter that sends to Sluicio
${pipelines.join("\n")}`;
}

// ServiceTrimList: the logs/traces flavor of the picker — one row per
// service from the usage report, uncovered-by-alerts first (that's the
// report's sort), with size estimates and safety flags. `services` is
// null while the usage report is unavailable (it's admin-only).
function ServiceTrimList({
  services,
  noun,
  isPicked,
  onToggle,
  renderControls,
  integrationsFor,
}: {
  services: UsageServiceRow[] | null;
  noun: string;
  isPicked: (service: string) => boolean;
  onToggle: (service: string) => void;
  renderControls: (service: string) => React.ReactNode;
  integrationsFor?: (service: string) => string[] | undefined;
}) {
  if (services === null) {
    return (
      <div className="placeholder" style={{ margin: 10 }}>
        The usage report couldn't be loaded (it needs admin rights) — no {noun} services to pick from.
      </div>
    );
  }
  if (services.length === 0) {
    return <div className="placeholder" style={{ margin: 10 }}>No services shipped {noun} in this window.</div>;
  }
  return (
    <div style={{ overflow: "auto", border: "1px solid var(--border)", borderRadius: 8, minHeight: 0, flex: 1 }}>
      {services.map((s) => {
        const integs = integrationsFor?.(s.service_name) ?? [];
        return (
          <div key={s.service_name} style={{ borderBottom: "1px solid var(--border)" }}>
            <label style={{ display: "flex", gap: 8, alignItems: "center", padding: "6px 10px", cursor: "pointer" }}>
              <input type="checkbox" checked={isPicked(s.service_name)} onChange={() => onToggle(s.service_name)} />
              <span className="mono" style={{ fontSize: 12.5, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                {s.service_name}
              </span>
              <span style={{ marginLeft: "auto", fontSize: 11, whiteSpace: "nowrap", display: "inline-flex", alignItems: "center", gap: 6 }}>
                <span className="muted">{formatBytes(s.est_bytes)}</span>
                {s.covered && (
                  <span className="m-rule-badge" style={{ padding: "1px 6px" }} title={`An alert rule watches this service's ${noun} — trimming may starve it`}>
                    🔔 alerted
                  </span>
                )}
                {integs.length > 0 && (
                  <span
                    className="m-rule-badge"
                    style={{ padding: "1px 6px" }}
                    title={`Feeds integration${integs.length > 1 ? "s" : ""} ${integs.join(", ")} — dropping its traces blinds their health checks; prefer sampling`}
                  >
                    ⚠ feeds {integs[0]}{integs.length > 1 ? ` +${integs.length - 1}` : ""}
                  </span>
                )}
                {renderControls(s.service_name)}
              </span>
            </label>
          </div>
        );
      })}
    </div>
  );
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
  // Which signal the left pane is picking from.
  const [tab, setTab] = useState<"metrics" | "logs" | "traces">("metrics");
  // Usage report feeds the logs/traces service lists (rows + sizes +
  // covered-by-alerts flags). Null when unavailable (non-admin) — the
  // logs/traces tabs then show a hint instead of a list.
  const [report, setReport] = useState<UsageReportResponse | null>(null);
  const [logRules, setLogRules] = useState<LogTrimRule[]>([]);
  const [traceRules, setTraceRules] = useState<TraceTrimRule[]>([]);
  // One kept-share percentage for all sampled trace services.
  const [samplePct, setSamplePct] = useState(10);
  // service name → integration names it feeds; dropping such a service's
  // traces blinds those integrations' health checks, so we warn.
  const [svcIntegrations, setSvcIntegrations] = useState<Map<string, string[]>>(new Map());
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
    api
      .usageReport(win)
      .then(setReport)
      .catch(() => setReport(null));
    api
      .listIntegrations(win)
      .then((r) => {
        const m = new Map<string, string[]>();
        for (const integ of r.integrations ?? []) {
          for (const svc of integ.services ?? []) {
            m.set(svc, [...(m.get(svc) ?? []), integ.name]);
          }
        }
        setSvcIntegrations(m);
      })
      .catch(() => setSvcIntegrations(new Map()));
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
    () => buildCollectorConfig([...excluded].sort(), [...prefixes], attrRules, logRules, traceRules, samplePct),
    [excluded, prefixes, attrRules, logRules, traceRules, samplePct],
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
    setLogRules([]);
    setTraceRules([]);
  };

  const toggleLogRule = (service: string) =>
    setLogRules((prev) =>
      prev.some((r) => r.service === service)
        ? prev.filter((r) => r.service !== service)
        : [...prev, { service, floor: "warn" }],
    );
  const setLogFloor = (service: string, floor: LogTrimRule["floor"]) =>
    setLogRules((prev) => prev.map((r) => (r.service === service ? { ...r, floor } : r)));
  const toggleTraceRule = (service: string) =>
    setTraceRules((prev) => {
      if (prev.some((r) => r.service === service)) return prev.filter((r) => r.service !== service);
      // Services feeding an integration default to the safe lever.
      const mode = svcIntegrations.has(service) ? "sample" : "drop";
      return [...prev, { service, mode }];
    });
  const setTraceMode = (service: string, mode: TraceTrimRule["mode"]) =>
    setTraceRules((prev) => prev.map((r) => (r.service === service ? { ...r, mode } : r)));

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
          <span>Trim ingestion · exclude telemetry from Sluicio</span>
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
          {/* signal picker */}
          <div style={{ display: "flex", flexDirection: "column", minHeight: 0 }}>
            <div style={{ display: "flex", gap: 6, marginBottom: 8 }}>
              {(["metrics", "logs", "traces"] as const).map((t) => {
                const n = t === "metrics" ? excluded.size + prefixes.size + attrRules.length : t === "logs" ? logRules.length : traceRules.length;
                return (
                  <button
                    key={t}
                    type="button"
                    className={`btn btn--sm${tab === t ? " btn--primary" : ""}`}
                    onClick={() => setTab(t)}
                  >
                    {t[0].toUpperCase() + t.slice(1)}
                    {n > 0 ? ` · ${n}` : ""}
                  </button>
                );
              })}
              <button className="btn btn--sm" type="button" onClick={clearAll} style={{ marginLeft: "auto" }}>
                Clear
              </button>
            </div>
            {tab === "metrics" && (
            <>
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
            </>
            )}

            {tab === "logs" && (
              <ServiceTrimList
                services={report?.logs.services ?? null}
                noun="logs"
                isPicked={(s) => logRules.some((r) => r.service === s)}
                onToggle={toggleLogRule}
                renderControls={(s) => {
                  const rule = logRules.find((r) => r.service === s);
                  if (!rule) return null;
                  return (
                    <select
                      className="toolbar__select"
                      value={rule.floor}
                      onChange={(e) => setLogFloor(s, e.target.value as LogTrimRule["floor"])}
                      onClick={(e) => e.stopPropagation()}
                      aria-label={`Log trim mode for ${s}`}
                      style={{ fontSize: 11, padding: "1px 4px" }}
                    >
                      <option value="warn">drop below WARN</option>
                      <option value="error">drop below ERROR</option>
                      <option value="all">drop everything</option>
                    </select>
                  );
                }}
              />
            )}

            {tab === "traces" && (
              <>
                {traceRules.some((r) => r.mode === "sample") && (
                  <div style={{ display: "flex", gap: 6, alignItems: "center", marginBottom: 8, fontSize: 12 }}>
                    <span className="muted">Sampled services keep</span>
                    <select
                      className="toolbar__select"
                      value={samplePct}
                      onChange={(e) => setSamplePct(Number(e.target.value))}
                      aria-label="Kept share of sampled traces"
                      style={{ fontSize: 12, padding: "1px 4px" }}
                    >
                      {[5, 10, 25, 50].map((p) => (
                        <option key={p} value={p}>{p}%</option>
                      ))}
                    </select>
                    <span className="muted">of their traces</span>
                  </div>
                )}
                <ServiceTrimList
                  services={report?.traces.services ?? null}
                  noun="traces"
                  isPicked={(s) => traceRules.some((r) => r.service === s)}
                  onToggle={toggleTraceRule}
                  integrationsFor={(s) => svcIntegrations.get(s)}
                  renderControls={(s) => {
                    const rule = traceRules.find((r) => r.service === s);
                    if (!rule) return null;
                    return (
                      <select
                        className="toolbar__select"
                        value={rule.mode}
                        onChange={(e) => setTraceMode(s, e.target.value as TraceTrimRule["mode"])}
                        onClick={(e) => e.stopPropagation()}
                        aria-label={`Trace trim mode for ${s}`}
                        style={{ fontSize: 11, padding: "1px 4px" }}
                      >
                        <option value="sample">sample</option>
                        <option value="drop">drop all</option>
                      </select>
                    );
                  }}
                />
              </>
            )}
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
              <button className="btn btn--sm btn--primary" type="button" onClick={copy} disabled={excluded.size === 0 && prefixes.size === 0 && attrRules.length === 0 && logRules.length === 0 && traceRules.length === 0}>
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
          Sluicio doesn't enforce this — paste the processors into your OpenTelemetry Collector's pipelines to stop
          this telemetry being sent. Anything watched by an alert rule is flagged 🔔 so you don't drop what you
          monitor by accident. Metrics: use <b>attrs</b> on a row to drop single series. Logs: severity floors keep
          warnings/errors flowing. Traces: services feeding an integration are flagged ⚠ — prefer <b>sample</b> there,
          since dropping their spans blinds the integration's health checks.
        </div>
      </div>
    </div>
  );
}
