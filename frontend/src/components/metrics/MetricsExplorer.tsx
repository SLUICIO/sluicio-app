// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The metric explorer: search every OTLP metric by name, narrow by type and by
// attribute filters, and read each metric's current value + sparkline + series
// count. Picking a metric opens a drawer with the windowed chart, per-metric
// attributes, and an alert-rule builder.
//
// Reused in two places: the global Metrics page (unscoped) and a service's
// Metrics tab (scoped to that one service via `service`). When scoped, group-by
// and trim are hidden — both are org-wide concerns — and the page chrome is
// dropped (`embedded`) since the service tab already provides context.

import { useEffect, useMemo, useState } from "react";
import { api } from "../../api/client";
import type {
  LogAttrFilter,
  LogFieldEntry,
  MetricCatalogEntry,
  MetricCatalogRichResponse,
  MetricGroup,
} from "../../api/types";
import AttributeSuggest from "../logs/AttributeSuggest";
import FilterChip from "../logs/FilterChip";
import GroupByControl, { type GroupValue } from "../groups/GroupByControl";
import GroupRollup from "../groups/GroupRollup";
import AlertBuilder from "./AlertBuilder";
import MetricRow from "./MetricRow";
import MetricsDrawer from "./MetricsDrawer";
import TrimIngestionPanel from "./TrimIngestionPanel";
import { formatNumber } from "../../lib/format";
import { useTimeWindow } from "../../lib/useTimeWindow";

const TYPE_TABS: { label: string; value: string }[] = [
  { label: "All", value: "all" },
  { label: "Counter", value: "counter" },
  { label: "Gauge", value: "gauge" },
  { label: "Histogram", value: "histogram" },
];

const GROUP_DIMS = [
  { value: "service", label: "Service" },
  { value: "integration", label: "Integration" },
  { value: "type", label: "Type" },
  { value: "attribute", label: "Attribute" },
];

function MetricTable({
  rows,
  selectedName,
  onSelect,
}: {
  rows: MetricCatalogEntry[];
  selectedName?: string;
  onSelect: (m: MetricCatalogEntry) => void;
}) {
  return (
    <div className="mtbl">
      <div className="mtbl-head">
        <div>Name</div>
        <div>Type</div>
        <div className="th-right">Value</div>
        <div>Window</div>
        <div>Series</div>
        <div className="th-rules">Active rules</div>
      </div>
      <div className="mtbl-body">
        {rows.map((m) => (
          <MetricRow key={m.name} entry={m} selected={selectedName === m.name} onClick={() => onSelect(m)} />
        ))}
      </div>
    </div>
  );
}

export default function MetricsExplorer({ service, embedded }: { service?: string; embedded?: boolean }) {
  const [windowVal] = useTimeWindow();

  const [resp, setResp] = useState<MetricCatalogRichResponse | null>(null);
  const [fields, setFields] = useState<LogFieldEntry[]>([]);
  const [query, setQuery] = useState(() => new URLSearchParams(window.location.search).get("metric") ?? "");
  const [debouncedQuery, setDebouncedQuery] = useState(() => new URLSearchParams(window.location.search).get("metric") ?? "");
  const [mtype, setMtype] = useState("all");
  const [chips, setChips] = useState<LogAttrFilter[]>(() => {
    try {
      const raw = new URLSearchParams(window.location.search).get("mattr");
      if (!raw) return [];
      const parsed = JSON.parse(raw);
      if (!Array.isArray(parsed)) return [];
      return parsed
        .filter((a) => a && a.key && a.value)
        .map((a) => ({ key: String(a.key), op: (a.op || "eq") as LogAttrFilter["op"], value: String(a.value) }));
    } catch {
      return [];
    }
  });
  const [selectedEntry, setSelectedEntry] = useState<MetricCatalogEntry | null>(null);
  const [loading, setLoading] = useState(true);
  const [reloadKey, setReloadKey] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [attrOpen, setAttrOpen] = useState(false);
  const [trimOpen, setTrimOpen] = useState(false);
  const [group, setGroup] = useState<GroupValue>({ by: "none", key: "" });
  const [groups, setGroups] = useState<MetricGroup[]>([]);
  const [groupsLoading, setGroupsLoading] = useState(false);

  // Group-by + trim are org-wide affordances; hide them when scoped to one
  // service (the catalog endpoint scopes, but the group endpoint doesn't).
  const allowGroups = !service;
  const grouped = allowGroups && group.by !== "none" && (group.by !== "attribute" || group.key !== "");

  const focusedMetric = useMemo(() => {
    if (selectedEntry) return selectedEntry.name;
    const q = query.trim();
    return q && (resp?.metrics ?? []).some((m) => m.name === q) ? q : "";
  }, [selectedEntry, query, resp]);

  // Debounce the search box so typing filters server-side (the backend
  // caps the catalog) without a request per keystroke.
  useEffect(() => {
    const t = setTimeout(() => setDebouncedQuery(query), 300);
    return () => clearTimeout(t);
  }, [query]);

  useEffect(() => {
    setLoading(true);
    setError(null);
    api
      .metricCatalog(windowVal, { q: debouncedQuery, type: mtype, attrs: chips, service, limit: 100 })
      .then(setResp)
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  }, [windowVal, debouncedQuery, mtype, chips, service, reloadKey]);

  useEffect(() => {
    api
      .metricFields(windowVal, focusedMetric || undefined)
      .then((r) => setFields(r.fields ?? []))
      .catch(() => setFields([]));
  }, [windowVal, focusedMetric]);

  useEffect(() => {
    if (!grouped) {
      setGroups([]);
      return;
    }
    setGroupsLoading(true);
    api
      .metricGroups(windowVal, group.by, { key: group.key, q: query || undefined, type: mtype, attrs: chips })
      .then((r) => setGroups(r.groups ?? []))
      .catch(() => setGroups([]))
      .finally(() => setGroupsLoading(false));
  }, [grouped, group.by, group.key, windowVal, query, mtype, chips]);

  const metrics = useMemo(() => resp?.metrics ?? [], [resp]);
  const visible = useMemo(
    () => (query ? metrics.filter((m) => m.name.toLowerCase().includes(query.toLowerCase())) : metrics),
    [metrics, query],
  );
  const recent = useMemo(() => fields.slice(0, 4).map((f) => f.key), [fields]);
  const attrKeys = useMemo(() => fields.map((f) => f.key), [fields]);

  const reload = () =>
    api.metricCatalog(windowVal, { q: debouncedQuery, type: mtype, attrs: chips, service, limit: 100 }).then(setResp).catch(() => {});

  const addFilter = (f: LogAttrFilter) => {
    setChips((cur) => (cur.some((c) => c.key === f.key && c.op === f.op && c.value === f.value) ? cur : [...cur, f]));
    setAttrOpen(false);
  };
  const removeFilter = (i: number) => setChips((cur) => cur.filter((_, j) => j !== i));

  // Toggle an `key = value` eq filter — used by the drawer's attribute list
  // to drill the selected metric down to one value (one queue, one pod…).
  const isEqFilterActive = (key: string, value: string) =>
    chips.some((c) => c.key === key && c.op === "eq" && c.value === value);
  const toggleEqFilter = (key: string, value: string) =>
    setChips((cur) => {
      const i = cur.findIndex((c) => c.key === key && c.op === "eq" && c.value === value);
      return i >= 0 ? cur.filter((_, j) => j !== i) : [...cur, { key, op: "eq", value }];
    });

  // Keep the drawer's stats/chart in step with the active filters: when a
  // chip narrows the catalog, re-bind the selected metric to its freshly
  // scoped row (same name) so the header value reflects the drill-down.
  useEffect(() => {
    setSelectedEntry((cur) => {
      if (!cur) return cur;
      const fresh = (resp?.metrics ?? []).find((m) => m.name === cur.name);
      return fresh ?? cur;
    });
  }, [resp]);

  const loadGroupMetrics = (g: MetricGroup): Promise<MetricCatalogEntry[]> => {
    const opts: { q?: string; type?: string; attrs?: LogAttrFilter[]; service?: string; integration?: string } = {
      q: query || undefined,
      type: mtype,
      attrs: chips,
      service,
    };
    if (group.by === "service") opts.service = g.key;
    else if (group.by === "integration") opts.integration = g.key;
    else if (group.by === "type") opts.type = g.key;
    else if (group.by === "attribute") opts.attrs = [...chips, { key: group.key, op: "eq", value: g.key }];
    return api.metricCatalog(windowVal, opts).then((r) => r.metrics ?? []);
  };

  return (
    <div>
      {!embedded && (
        <div className="page__header" style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 16, flexWrap: "wrap" }}>
          <div>
            <h1 className="page__title">Metrics</h1>
            <p className="page__subtitle">
              Explore time-series metrics and wire alerts to channels. Review them regularly and keep only
              the metrics you actually act on — every series you ingest and store has a cost. Backed by the
              OpenTelemetry metrics signal.
            </p>
          </div>
          <div style={{ display: "flex", alignItems: "center", gap: 16 }}>
            <div className="m-head-stats">
              <div className="m-headstat">
                <span className="m-headstat-v">{formatNumber(metrics.length)}</span>
                <span className="m-headstat-k">indexed metrics</span>
              </div>
              <div className="m-headstat">
                <span className="m-headstat-v">{formatNumber(resp?.total_series ?? 0)}</span>
                <span className="m-headstat-k">active series</span>
              </div>
              <div className="m-headstat">
                <span className="m-headstat-v">{formatNumber(resp?.rule_count ?? 0)}</span>
                <span className="m-headstat-k">alert rules</span>
              </div>
            </div>
            <button className="btn" onClick={() => setReloadKey((k) => k + 1)} disabled={loading}>
              {loading ? "Loading…" : "Refresh"}
            </button>
          </div>
        </div>
      )}

      {/* Filter bar */}
      <div className="card" style={{ padding: "10px 12px", overflow: "visible" }}>
        <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
          <div style={{ position: "relative", flex: 1, minWidth: 280, display: "flex", alignItems: "center" }}>
            <span aria-hidden style={{ position: "absolute", left: 10, color: "var(--muted)" }}>⌕</span>
            <input
              className="search__input mono"
              style={{ paddingLeft: 30, fontSize: 13 }}
              placeholder={service ? `Search ${service}'s metrics…` : "Search metrics by name…"}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>

          <div className="level-seg" role="tablist" aria-label="Metric type">
            {TYPE_TABS.map((t) => (
              <button
                key={t.value}
                type="button"
                role="tab"
                aria-checked={mtype === t.value}
                className="level-seg__btn"
                onClick={() => setMtype(t.value)}
              >
                {t.label}
              </button>
            ))}
          </div>

          {allowGroups && <GroupByControl value={group} onChange={setGroup} dims={GROUP_DIMS} attrKeys={attrKeys} />}
        </div>

        <div style={{ display: "flex", gap: 6, alignItems: "center", flexWrap: "wrap", marginTop: 8 }}>
          {chips.map((c, i) => (
            <FilterChip key={`${c.key}-${c.op}-${i}`} k={c.key} op={c.op} value={c.value} accent={i === 0} onRemove={() => removeFilter(i)} />
          ))}
          <div style={{ position: "relative" }}>
            <button type="button" className="addfilter" aria-expanded={attrOpen} onClick={() => setAttrOpen((o) => !o)}>
              + Add attribute filter
            </button>
            {attrOpen && (
              <AttributeSuggest
                fields={fields}
                recent={recent}
                window={windowVal}
                onPick={addFilter}
                onClose={() => setAttrOpen(false)}
                fetchValues={(key, win) => api.metricAttributeValues(key, win, 50, focusedMetric || undefined)}
                keyPlaceholder="Filter attributes by name…"
                footHint={focusedMetric ? `attributes on ${focusedMetric}` : "attributes are series labels, not points"}
              />
            )}
          </div>

          {!service && (
            <button
              type="button"
              className="addfilter"
              style={{ marginLeft: "auto" }}
              onClick={() => setTrimOpen(true)}
              title="Generate an OTel Collector config to drop metrics you don't act on"
            >
              ⚙ Trim ingestion
            </button>
          )}
        </div>
      </div>

      {error && <div className="alert alert--error" style={{ marginTop: 12 }}>Failed to load metrics: {error}</div>}

      <div className={`logs-split metrics-split ${selectedEntry ? "has-detail" : ""}`} style={{ marginTop: 12 }}>
        <div style={{ minWidth: 0 }}>
          {grouped ? (
            <GroupRollup<MetricGroup, MetricCatalogEntry>
              groups={groups}
              loading={groupsLoading}
              emptyLabel="No groups in this window."
              cacheKey={JSON.stringify({ query, mtype, chips, groupBy: group.by, groupKey: group.key, windowVal })}
              groupKey={(g) => g.key}
              renderLabel={(g) => g.key}
              renderStats={(g) => (
                <>
                  <span><span className="n">{formatNumber(g.metric_count)}</span> metrics</span>
                  <span><span className="n">{formatNumber(g.series_count)}</span> series</span>
                </>
              )}
              loadItems={loadGroupMetrics}
              renderItems={(items) =>
                items.length === 0 ? (
                  <div className="placeholder" style={{ margin: 10 }}>No metrics.</div>
                ) : (
                  <MetricTable rows={items} selectedName={selectedEntry?.name} onSelect={setSelectedEntry} />
                )
              }
            />
          ) : allowGroups && group.by === "attribute" && !group.key ? (
            <div className="mtbl"><div className="placeholder" style={{ margin: 12 }}>Choose an attribute to group by.</div></div>
          ) : loading && metrics.length === 0 ? (
            <div className="mtbl"><div className="placeholder" style={{ margin: 12 }}>Loading…</div></div>
          ) : visible.length === 0 ? (
            <div className="mtbl">
              <div className="placeholder" style={{ margin: 12 }}>
                {metrics.length === 0 ? "No metrics in this window." : "No metrics match the filters."}
              </div>
            </div>
          ) : (
            <>
              <MetricTable
                rows={visible}
                selectedName={selectedEntry?.name}
                onSelect={(m) => setSelectedEntry((cur) => (cur?.name === m.name ? null : m))}
              />
              {metrics.length >= 100 && (
                <div className="placeholder" style={{ margin: 10, fontSize: 12 }}>
                  Showing the first 100 metrics — refine your search to narrow the list.
                </div>
              )}
            </>
          )}
        </div>

        {selectedEntry && resp && (
          <MetricsDrawer
            entry={selectedEntry}
            chips={chips}
            window={resp.window}
            range={windowVal}
            breached={selectedEntry.threshold != null && selectedEntry.value > selectedEntry.threshold}
            threshold={selectedEntry.threshold}
            onClose={() => setSelectedEntry(null)}
            onToggleFilter={toggleEqFilter}
            isFilterActive={isEqFilterActive}
            builder={
              <AlertBuilder
                metricName={selectedEntry.name}
                unit={selectedEntry.unit}
                attrs={chips}
                onChanged={reload}
                defaultService={service}
              />
            }
          />
        )}
      </div>

      {trimOpen && <TrimIngestionPanel window={windowVal} onClose={() => setTrimOpen(false)} />}
    </div>
  );
}
