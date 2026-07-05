// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Usage — how much telemetry this cell is storing, across the three
// signals: trace spans, metric data points (+ active series), and log
// records. The API returns per-service counts (visibility-scoped) plus the
// actual on-disk size of each table; this page shows grand totals + real
// storage size, plus a breakdown grouped by service or rolled up by
// integration. Per-service size is an estimate (on-disk bytes ÷ rows ×
// the group's rows) — exact per-service compressed bytes aren't available.
// Read-only and org-admin only (route + API both gated).

import { useEffect, useMemo, useState } from "react";
import { api } from "../api/client";
import type { Integration, IntegrationUsage, UsageVolumeResponse } from "../api/types";
import TimeWindowPicker from "../components/TimeWindowPicker";
import { SortableTh } from "../components/primitives";
import { formatBytes, formatNumber } from "../lib/format";
import { usePageTitle } from "../lib/usePageTitle";
import { useTableSort } from "../lib/useTableSort";
import { useTimeWindow } from "../lib/useTimeWindow";

type GroupBy = "none" | "service" | "integration";
type SortKey = "group" | "spans" | "metric_points" | "metric_series" | "logs" | "bytes" | "total";

interface Row {
  group: string;
  spans: number;
  metric_points: number;
  metric_series: number;
  logs: number;
  bytes: number; // estimated on-disk size for this group's rows
  total: number; // spans + metric_points + logs (record count, not series/bytes)
}

const UNSET = "(unset)";
const UNASSIGNED = "(unassigned)";

export default function Usage() {
  usePageTitle("Usage");
  const [windowVal] = useTimeWindow();
  const [data, setData] = useState<UsageVolumeResponse | null>(null);
  const [integrations, setIntegrations] = useState<Integration[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const [groupBy, setGroupBy] = useState<GroupBy>("none");
  // false (default) = total stored at rest across the DBs; true = bound to
  // the selected time window.
  const [windowed, setWindowed] = useState(false);

  useEffect(() => {
    setLoading(true);
    setError(null);
    Promise.all([api.usageVolume(windowVal, windowed), api.listIntegrations(windowVal)])
      .then(([usage, integ]) => {
        setData(usage);
        setIntegrations(integ.integrations ?? []);
      })
      .catch((e) => setError(String((e as Error).message ?? e)))
      .finally(() => setLoading(false));
  }, [windowVal, windowed, reloadKey]);

  // Bytes-per-row per signal, from the table's real on-disk size ÷ its row
  // count. Used to estimate per-group size from row counts.
  const bpr = useMemo(() => {
    const s = data?.storage;
    const per = (sig?: { bytes: number; rows: number }) => (sig && sig.rows > 0 ? sig.bytes / sig.rows : 0);
    return { spans: per(s?.spans), metric: per(s?.metric_points), logs: per(s?.logs) };
  }, [data]);
  const estBytes = useMemo(
    () => (spans: number, mp: number, logs: number) => spans * bpr.spans + mp * bpr.metric + logs * bpr.logs,
    [bpr],
  );

  // Build the breakdown rows for the active grouping. Service rows come
  // straight from the API; integration rows roll member services up — a
  // service that belongs to more than one integration counts toward each,
  // so per-integration rows can sum above the grand totals (the cards
  // above stay canonical). Services in no integration land in (unassigned).
  const rows = useMemo<Row[]>(() => {
    if (groupBy === "none") return [];
    const svcs = data?.services ?? [];
    if (groupBy === "service") {
      return svcs.map((s) => ({
        group: s.service || UNSET,
        spans: s.spans,
        metric_points: s.metric_points,
        metric_series: s.metric_series,
        logs: s.logs,
        bytes: estBytes(s.spans, s.metric_points, s.logs),
        total: s.spans + s.metric_points + s.logs,
      }));
    }
    const byService = new Map(svcs.map((s) => [s.service, s]));
    const assigned = new Set<string>();
    const mk = (group: string, spans: number, mp: number, series: number, logs: number): Row => ({
      group,
      spans,
      metric_points: mp,
      metric_series: series,
      logs,
      bytes: estBytes(spans, mp, logs),
      total: spans + mp + logs,
    });
    const out: Row[] = [];
    for (const intg of integrations) {
      let spans = 0;
      let mp = 0;
      let series = 0;
      let logs = 0;
      for (const name of intg.services ?? []) {
        assigned.add(name);
        const s = byService.get(name);
        if (s) {
          spans += s.spans;
          mp += s.metric_points;
          series += s.metric_series;
          logs += s.logs;
        }
      }
      if (spans + mp + logs > 0) out.push(mk(intg.name, spans, mp, series, logs));
    }
    let uSpans = 0;
    let uMp = 0;
    let uSeries = 0;
    let uLogs = 0;
    for (const s of svcs) {
      if (assigned.has(s.service)) continue;
      uSpans += s.spans;
      uMp += s.metric_points;
      uSeries += s.metric_series;
      uLogs += s.logs;
    }
    if (uSpans + uMp + uLogs > 0) out.push(mk(UNASSIGNED, uSpans, uMp, uSeries, uLogs));
    return out;
  }, [data, integrations, groupBy, estBytes]);

  const { sortedRows, sort, toggleSort } = useTableSort<Row, SortKey>(
    rows,
    {
      group: (r) => r.group.toLowerCase(),
      spans: (r) => r.spans,
      metric_points: (r) => r.metric_points,
      metric_series: (r) => r.metric_series,
      logs: (r) => r.logs,
      bytes: (r) => r.bytes,
      total: (r) => r.total,
    },
    { key: "bytes", dir: "desc" },
  );

  const groupHeader = groupBy === "service" ? "Service" : "Integration";

  const exportCsv = () => {
    const head = [groupHeader, "Spans", "Metric data points", "Active series", "Logs", "Est. size (bytes)", "Records"];
    const lines = [
      head.join(","),
      ...sortedRows.map((r) =>
        [r.group, r.spans, r.metric_points, r.metric_series, r.logs, Math.round(r.bytes), r.total]
          .map((c) => (typeof c === "string" && /[",\n]/.test(c) ? `"${c.replace(/"/g, '""')}"` : c))
          .join(","),
      ),
    ];
    const blob = new Blob([lines.join("\n")], { type: "text/csv" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `usage-${groupBy}-${windowed ? windowVal : "all"}.csv`;
    a.click();
    URL.revokeObjectURL(url);
  };

  const totals = data?.totals;
  const storage = data?.storage;
  // Headline size per signal: the exact on-disk total when showing
  // everything stored; an estimate (count × bytes-per-row) when windowed.
  const sizeFor = (count?: number, exact?: number, perRow?: number): string | undefined => {
    if (!data) return undefined;
    if (!windowed) return formatBytes(exact ?? 0);
    return `≈ ${formatBytes((count ?? 0) * (perRow ?? 0))}`;
  };

  return (
    <div>
      <div className="page__header" style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 16, flexWrap: "wrap" }}>
        <div>
          <h1 className="page__title">Usage</h1>
          <p className="page__subtitle">
            How much telemetry this cell is storing — trace spans, metric data points, and log records.
            {windowed
              ? " Bounded to the selected time window."
              : " Total at rest across the databases (after retention)."}{" "}
            Scoped to the services you can see.
          </p>
        </div>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          {windowed && <TimeWindowPicker />}
          <button className="btn" onClick={() => setReloadKey((k) => k + 1)} disabled={loading}>
            {loading ? "Loading…" : "Refresh"}
          </button>
        </div>
      </div>

      <label style={{ display: "inline-flex", alignItems: "center", gap: 8, fontSize: 13, marginBottom: 16, cursor: "pointer" }}>
        <input type="checkbox" checked={windowed} onChange={(e) => setWindowed(e.target.checked)} />
        Limit to selected time window
        <span className="muted" style={{ fontSize: 12 }}>
          {windowed ? "(counting only what arrived in the window)" : "(off — counting everything stored)"}
        </span>
      </label>

      {error && <div className="alert alert--error" style={{ marginBottom: 12 }}>Failed to load usage: {error}</div>}

      {/* licensed integration usage — the X-of-Y KPI against the license cap */}
      <IntegrationKpiCard usage={data?.integrations} loading={loading && !data} />

      {/* grand totals */}
      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))", gap: 12, marginBottom: 16 }}>
        <TotalCard
          label="Trace spans"
          value={totals?.spans}
          size={sizeFor(totals?.spans, storage?.spans.bytes, bpr.spans)}
          loading={loading && !data}
        />
        <TotalCard
          label="Metric data points"
          value={totals?.metric_points}
          size={sizeFor(totals?.metric_points, storage?.metric_points.bytes, bpr.metric)}
          note={data ? `${formatNumber(totals?.metric_series ?? 0)} active series` : undefined}
          loading={loading && !data}
        />
        <TotalCard
          label="Log records"
          value={totals?.logs}
          size={sizeFor(totals?.logs, storage?.logs.bytes, bpr.logs)}
          loading={loading && !data}
        />
      </div>

      {/* group-by control — always visible so grouping can be turned on/off */}
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12, flexWrap: "wrap" }}>
        <span className="muted" style={{ fontSize: 12.5 }}>Group by</span>
        <div className="level-seg" role="tablist" aria-label="Group by">
          {(["none", "service", "integration"] as GroupBy[]).map((g) => (
            <button
              key={g}
              type="button"
              role="tab"
              aria-checked={groupBy === g}
              className="level-seg__btn"
              onClick={() => setGroupBy(g)}
            >
              {g === "none" ? "None" : g === "service" ? "Service" : "Integration"}
            </button>
          ))}
        </div>
        {groupBy === "none" && (
          <span className="muted" style={{ fontSize: 12 }}>
            Showing overall totals only — pick a dimension to break them down.
          </span>
        )}
      </div>

      {/* breakdown */}
      {groupBy !== "none" && (
        <div className="card">
          <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "10px 12px", flexWrap: "wrap" }}>
            <span className="muted" style={{ fontSize: 12 }}>
              {sortedRows.length} {groupBy === "service" ? "services" : "integrations"}
            </span>
            <button className="btn btn--sm" style={{ marginLeft: "auto" }} onClick={exportCsv} disabled={sortedRows.length === 0}>
              Export CSV
            </button>
          </div>

          <table className="table">
            <thead>
              <tr>
                <SortableTh sortKey="group" state={sort} onSort={toggleSort}>{groupHeader}</SortableTh>
                <SortableTh sortKey="spans" state={sort} onSort={toggleSort} className="num">Spans</SortableTh>
                <SortableTh sortKey="metric_points" state={sort} onSort={toggleSort} className="num">Metric points</SortableTh>
                <SortableTh sortKey="metric_series" state={sort} onSort={toggleSort} className="num">Series</SortableTh>
                <SortableTh sortKey="logs" state={sort} onSort={toggleSort} className="num">Logs</SortableTh>
                <SortableTh
                  sortKey="bytes"
                  state={sort}
                  onSort={toggleSort}
                  className="num"
                  title="Estimated on-disk size: table size ÷ rows × this group's rows. Exact per-service compressed size isn't available."
                >
                  Est. size
                </SortableTh>
              </tr>
            </thead>
            <tbody>
              {sortedRows.length === 0 ? (
                <tr>
                  <td colSpan={6} className="muted" style={{ padding: 16, textAlign: "center" }}>
                    {loading ? "Loading…" : "No telemetry stored for this selection."}
                  </td>
                </tr>
              ) : (
                sortedRows.map((r) => (
                  <tr key={r.group}>
                    <td>{r.group}</td>
                    <td className="num">{formatNumber(r.spans)}</td>
                    <td className="num">{formatNumber(r.metric_points)}</td>
                    <td className="num">{r.metric_series ? formatNumber(r.metric_series) : "—"}</td>
                    <td className="num">{formatNumber(r.logs)}</td>
                    <td className="num"><strong>{formatBytes(r.bytes)}</strong></td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      )}

      <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>
        {groupBy === "integration" && (
          <>
            A service in more than one integration is counted toward each, so these rows can sum above the totals
            above.{" "}
          </>
        )}
        “Series” is distinct metric + label-set combinations (Grafana “active series”). Per-row size is estimated from
        each table's real on-disk size; the cards show the exact stored size.
      </p>
    </div>
  );
}

// The licensed-usage KPI: "5 of 25" (or "1 of unlimited") monitored entities —
// integrations + systems — with a breakdown sub-line. Tinted neutral /
// warning (≥80% of cap) / error (cap reached).
function IntegrationKpiCard({ usage, loading }: { usage?: IntegrationUsage; loading: boolean }) {
  if (loading || !usage) {
    return (
      <div className="card" style={{ padding: "12px 16px", marginBottom: 16 }}>
        <span className="muted" style={{ fontSize: 13 }}>
          {loading ? "Loading usage…" : "Usage unavailable."}
        </span>
      </div>
    );
  }
  const { used, integration_count, system_count, limit, unlimited, over_limit } = usage;
  const near = !unlimited && !over_limit && limit > 0 && used >= Math.ceil(limit * 0.8);
  const tone: "ok" | "warn" | "error" = unlimited ? "ok" : over_limit ? "error" : near ? "warn" : "ok";
  const palette = {
    ok: { bg: "var(--surface-2)", border: "var(--border)", ink: "var(--ink)" },
    warn: { bg: "var(--warn-soft)", border: "var(--warn)", ink: "var(--warn-ink)" },
    error: { bg: "var(--err-soft)", border: "var(--err)", ink: "var(--err-ink)" },
  }[tone];
  const value = unlimited
    ? `${formatNumber(used)} of unlimited`
    : `${formatNumber(used)} of ${formatNumber(limit)}`;
  const left = unlimited ? 0 : limit - used;
  const msg = unlimited
    ? "Unlimited on your plan."
    : over_limit
      ? "You've reached your plan's limit — upgrade to add more."
      : near
        ? `Approaching your plan limit — ${left} left.`
        : "";
  const breakdown = `${formatNumber(integration_count)} integration${integration_count === 1 ? "" : "s"} + ${formatNumber(system_count)} system${system_count === 1 ? "" : "s"}`;
  return (
    <div
      className="card"
      style={{ padding: "12px 16px", marginBottom: 16, background: palette.bg, borderLeft: `4px solid ${palette.border}` }}
    >
      <div style={{ display: "flex", alignItems: "baseline", gap: 12, flexWrap: "wrap" }}>
        <span style={{ fontSize: 20, fontWeight: 700, color: palette.ink, fontVariantNumeric: "tabular-nums" }}>{value}</span>
        <span className="muted" style={{ fontSize: 12.5 }}>integrations &amp; systems</span>
        {msg && (
          <span style={{ fontSize: 12.5, color: palette.ink, marginLeft: "auto" }}>{msg}</span>
        )}
      </div>
      <div className="muted" style={{ fontSize: 12, marginTop: 4 }}>{breakdown}</div>
    </div>
  );
}

function TotalCard({
  label,
  value,
  size,
  note,
  loading,
}: {
  label: string;
  value?: number;
  size?: string;
  note?: string;
  loading: boolean;
}) {
  return (
    <div className="card" style={{ padding: "14px 16px" }}>
      <div style={{ fontSize: 22, fontWeight: 700, fontVariantNumeric: "tabular-nums" }}>
        {loading ? "…" : formatNumber(value ?? 0)}
      </div>
      <div className="muted" style={{ fontSize: 12.5, marginTop: 2 }}>{label}</div>
      <div style={{ display: "flex", gap: 10, marginTop: 6, flexWrap: "wrap" }}>
        {size && (
          <span className="badge" style={{ fontSize: 11 }} title="On-disk (compressed) size">
            {size} on disk
          </span>
        )}
        {note && <span className="muted" style={{ fontSize: 11.5 }}>{note}</span>}
      </div>
    </div>
  );
}
