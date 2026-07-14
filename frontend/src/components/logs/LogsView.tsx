// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// LogsView — the reusable body of the Logs page. Renders the filter
// bar (free-text + severity + group-by + attribute chips), the volume
// histogram, and the keyset-paged virtualized log table (or group
// rollup when grouped).
//
// Two consumers today:
//   1. /logs           (LogsPage)          — global view, all integrations
//   2. /integrations/:id/logs (IntegrationLogs) — scoped to one integration
//
// The integration-scoped view passes `forcedIntegration=<name>` so every
// query carries `integration=<name>`. The cell-api intersects that with
// the caller's policy allowlist (G5), so a user only sees logs from
// services they're entitled to within the integration. The
// `forcedIntegration` filter is locked — no UI for removing it — and
// the "Integration" option is dropped from the group-by dropdown
// (redundant when everything is inside one integration).

import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../../api/client";
import type {
  AlertRuleInput,
  AlertSeverity,
  LogAttrFilter,
  LogCursor,
  LogEntry,
  LogFieldEntry,
  LogGroup,
  LogVolumeResponse,
  NotificationChannel,
} from "../../api/types";
import { EditDrawer } from "../primitives";
import { useCurrentUser } from "../../lib/useCurrentUser";
import AttributeSuggest from "./AttributeSuggest";
import FilterChip from "./FilterChip";
import SearchableSelect from "../SearchableSelect";
import GroupByControl, { type GroupValue } from "../groups/GroupByControl";
import GroupRollup from "../groups/GroupRollup";
import LevelBadge from "./LevelBadge";
import LevelToggle from "./LevelToggle";
import LogDetailsDrawer from "./LogDetailsDrawer";
import TraceDrawer from "../TraceDrawer";
import VolumeHistogram from "./VolumeHistogram";
import VirtualInfiniteList from "../VirtualInfiniteList";
import { severityBand } from "../../lib/severity";

// Shared style for the non-removable scope chips (integration / service),
// which double as links to the entity when its id is known.
const SCOPE_CHIP_STYLE: CSSProperties = {
  display: "inline-flex",
  alignItems: "center",
  gap: 4,
  padding: "3px 8px",
  background: "var(--primary-soft)",
  color: "var(--primary-ink)",
  border: "1px solid var(--primary)",
  borderRadius: 4,
  fontSize: 12,
  fontWeight: 600,
  textDecoration: "none",
};
import { useTimeWindow } from "../../lib/useTimeWindow";

const PAGE = 100;
const GRID = "120px 60px 160px minmax(280px, 1fr) minmax(220px, 1.2fr)";
const RECENT_KEY = "conduit.logs.recentAttrs";

const ALL_LOG_GROUP_DIMS = [
  { value: "service", label: "Service" },
  { value: "integration", label: "Integration" },
  { value: "severity", label: "Severity" },
  { value: "attribute", label: "Attribute" },
];

// Severity band → OTLP floor for expanding a band group. The rollup
// counts are exact; the expanded preview shows that band and above
// (the list endpoint takes a floor, not a range).
const BAND_FLOOR: Record<string, number> = { info: 0, warn: 13, error: 17, fatal: 21 };

function fmtTime(iso: string): string {
  const d = new Date(iso);
  const p = (n: number, w = 2) => String(n).padStart(w, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`;
}

function loadRecent(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(RECENT_KEY) || "[]");
    return Array.isArray(v) ? v.slice(0, 8) : [];
  } catch {
    return [];
  }
}

// A filter is "the same" if key+op match — adding service.name twice
// just updates the value rather than stacking duplicate chips.
function sameFilter(a: LogAttrFilter, b: LogAttrFilter) {
  return a.key === b.key && a.op === b.op;
}

const ellipsis: React.CSSProperties = { overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" };

export interface LogsViewProps {
  /**
   * When set, every query carries `integration=<name>` and the
   * "Integration" group-by option is suppressed. Used by the
   * IntegrationDetail logs tab.
   */
  forcedIntegration?: string;
  /**
   * The integration's id (when known, i.e. the integration logs tab).
   * Lets a log alert rule created here bind to the integration's health.
   */
  forcedIntegrationId?: string;
  /**
   * When set, every query carries `service=<name>` and the "Service"
   * group-by option is suppressed. Used by the ServiceDetail logs tab so
   * it gets the full Logs-page filtering scoped to one service.
   */
  forcedService?: string;
}

export default function LogsView({ forcedIntegration, forcedIntegrationId, forcedService }: LogsViewProps = {}) {
  const [windowVal, setWindow] = useTimeWindow();
  const [searchParams, setSearchParams] = useSearchParams();
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");
  const [alertOpen, setAlertOpen] = useState(false);

  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [cursor, setCursor] = useState<LogCursor | undefined>(undefined);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const loadingRef = useRef(false);

  // Filter state. Free text debounces; severity + attribute chips
  // re-query immediately. Initial values hydrate from the URL (?logq body,
  // ?logmin severity floor) so a deep link — e.g. a log health-check's
  // "open in logs" — lands pre-filtered. Dedicated param names avoid
  // colliding with the Traces tab's ?q / ?s.
  const initialLogFilters = useRef(new URLSearchParams(window.location.search)).current;
  const [qInput, setQInput] = useState(() => initialLogFilters.get("logq") ?? "");
  const [appliedQ, setAppliedQ] = useState(() => initialLogFilters.get("logq") ?? "");
  const [minSeverity, setMinSeverity] = useState(() => Number(initialLogFilters.get("logmin")) || 0);
  const [attrs, setAttrs] = useState<LogAttrFilter[]>(() =>
    initialLogFilters
      .getAll("logattr")
      .map((raw) => {
        try {
          const v = JSON.parse(raw);
          if (v && typeof v.key === "string" && typeof v.op === "string" && typeof v.value === "string") {
            return v as LogAttrFilter;
          }
        } catch {
          /* malformed param — drop it */
        }
        return null;
      })
      .filter((v): v is LogAttrFilter => v !== null),
  );

  const [fields, setFields] = useState<LogFieldEntry[]>([]);
  const [recent, setRecent] = useState<string[]>(loadRecent);

  // Service → integration(s) it belongs to, from persisted catalog
  // membership (window-independent). Built once from a single cheap call so
  // each log row can resolve its integration with an O(1) map lookup — no
  // per-row request. Skipped when the page is already scoped to one
  // integration (the integration is then a given).
  const [svcIntegrations, setSvcIntegrations] = useState<Map<string, { id: string; name: string }[]>>(new Map());
  useEffect(() => {
    if (forcedIntegration) return;
    api
      .listIntegrations("24h")
      .then((r) => {
        const m = new Map<string, { id: string; name: string }[]>();
        for (const intg of r.integrations ?? []) {
          for (const s of intg.services ?? []) {
            const arr = m.get(s);
            if (arr) arr.push({ id: intg.id, name: intg.name });
            else m.set(s, [{ id: intg.id, name: intg.name }]);
          }
        }
        setSvcIntegrations(m);
      })
      .catch(() => setSvcIntegrations(new Map()));
  }, [forcedIntegration]);
  const [suggestOpen, setSuggestOpen] = useState(false);
  const [selected, setSelected] = useState<LogEntry | null>(null);
  // setSearchParams goes through a ref because react-router does not
  // keep its identity stable across URL writes. ALL url-mirroring for
  // this page happens in ONE effect below — two writers in the same
  // commit stomp each other, because react-router's functional updater
  // computes from render-time params, not the pending navigation.
  const setSearchParamsRef = useRef(setSearchParams);
  useEffect(() => {
    setSearchParamsRef.current = setSearchParams;
  });
  // A log's trace context opens the trace as a slide-over blade (the
  // same TraceDrawer the integration Messages view uses) — inspecting
  // a trace shouldn't navigate away from the filtered log list.
  const [openTraceId, setOpenTraceId] = useState<string | null>(null);
  const [volume, setVolume] = useState<LogVolumeResponse | null>(null);
  const [volumeLoading, setVolumeLoading] = useState(true);

  const [group, setGroup] = useState<GroupValue>(() => {
    const raw = initialLogFilters.get("loggroup") ?? "";
    if (!raw) return { by: "none", key: "" };
    const [by, ...rest] = raw.split(":");
    return { by, key: rest.join(":") };
  });
  const [groups, setGroups] = useState<LogGroup[]>([]);
  const [groupsLoading, setGroupsLoading] = useState(false);
  const grouped = group.by !== "none" && (group.by !== "attribute" || group.key !== "");

  // Mirror the page state into the URL — the SINGLE writer for this
  // page's params (?logq body text, ?logmin severity floor, repeated
  // ?logattr JSON chips, ?loggroup, ?log open drawer) so a filtered
  // view is shareable by copying the address bar; the same params the
  // mount-time hydration consumes. replace: true keeps keystrokes out
  // of the back button. Deliberately one effect: a second concurrent
  // setSearchParams writer computes from stale render-time params and
  // reverts this one's write.
  useEffect(() => {
    setSearchParamsRef.current(
      (prev) => {
        const next = new URLSearchParams(prev);
        if (appliedQ) next.set("logq", appliedQ);
        else next.delete("logq");
        if (minSeverity) next.set("logmin", String(minSeverity));
        else next.delete("logmin");
        next.delete("logattr");
        for (const a of attrs) next.append("logattr", JSON.stringify(a));
        if (group.by !== "none") next.set("loggroup", group.key ? `${group.by}:${group.key}` : group.by);
        else next.delete("loggroup");
        if (selected?.log_id) next.set("log", selected.log_id);
        else next.delete("log");
        return next;
      },
      { replace: true },
    );
  }, [appliedQ, minSeverity, attrs, group, selected]);


  // Dropping "integration" from the dimension list when we're already
  // scoped to one integration — every row would land in the same group.
  const logGroupDims = useMemo(
    () =>
      ALL_LOG_GROUP_DIMS.filter(
        (d) =>
          !(forcedIntegration && d.value === "integration") &&
          !(forcedService && d.value === "service"),
      ),
    [forcedIntegration, forcedService],
  );

  const listHeight = useMemo(
    () => (typeof window !== "undefined" ? Math.max(360, window.innerHeight - 320) : 560),
    [],
  );

  // Debounce the free-text body search (250ms per the spec).
  useEffect(() => {
    const t = window.setTimeout(() => setAppliedQ(qInput.trim()), 250);
    return () => window.clearTimeout(t);
  }, [qInput]);

  // Attribute key catalog for the autocomplete.
  useEffect(() => {
    api.logFields(windowVal).then((r) => setFields(r.fields ?? [])).catch(() => setFields([]));
  }, [windowVal]);

  // Group rollups, when a dimension is selected.
  useEffect(() => {
    if (!grouped) {
      setGroups([]);
      return;
    }
    setGroupsLoading(true);
    api
      .logGroups(windowVal, group.by, {
        key: group.key,
        q: appliedQ || undefined,
        minSeverity: minSeverity || undefined,
        attrs,
        integration: forcedIntegration,
        service: forcedService,
      })
      .then((r) => setGroups(r.groups ?? []))
      .catch(() => setGroups([]))
      .finally(() => setGroupsLoading(false));
  }, [grouped, group.by, group.key, windowVal, appliedQ, minSeverity, attrs, forcedIntegration, forcedService]);

  const attrKeys = useMemo(() => fields.map((f) => f.key), [fields]);

  // groupItemsCacheKey is the "filters changed" signal we pass to
  // GroupRollup so it invalidates per-group cached entry lists when
  // the user adjusts severity, query, attributes, group-by, or the
  // time window. Without this the row counts updated but the expanded
  // entries stayed pinned to the prior filter (issue #7).
  const groupItemsCacheKey = useMemo(
    () =>
      JSON.stringify({
        q: appliedQ,
        minSeverity,
        attrs,
        groupBy: group.by,
        groupKey: group.key,
        windowVal,
        forcedIntegration,
        forcedService,
      }),
    [appliedQ, minSeverity, attrs, group.by, group.key, windowVal, forcedIntegration, forcedService],
  );

  // Load a group's logs on expand: the page filters + the group scope.
  const loadGroupLogs = (g: LogGroup): Promise<LogEntry[]> => {
    const opts: { q?: string; minSeverity?: number; service?: string; integration?: string; attrs?: LogAttrFilter[]; limit?: number } = {
      q: appliedQ || undefined,
      minSeverity: minSeverity || undefined,
      attrs,
      limit: 100,
      integration: forcedIntegration,
      service: forcedService,
    };
    if (group.by === "service") opts.service = g.key;
    else if (group.by === "integration") opts.integration = g.key;
    else if (group.by === "severity") opts.minSeverity = BAND_FLOOR[g.key] ?? (minSeverity || undefined);
    else if (group.by === "attribute") opts.attrs = [...attrs, { key: group.key, op: "eq", value: g.key }];
    return api.searchLogs(windowVal, opts).then((r) => r.logs ?? []);
  };

  // Volume histogram — same filters as the table (no cursor).
  useEffect(() => {
    setVolumeLoading(true);
    api
      .logVolume(windowVal, {
        q: appliedQ || undefined,
        minSeverity: minSeverity || undefined,
        attrs,
        integration: forcedIntegration,
        service: forcedService,
      })
      .then(setVolume)
      .catch(() => setVolume(null))
      .finally(() => setVolumeLoading(false));
  }, [windowVal, appliedQ, minSeverity, attrs, forcedIntegration, forcedService, reloadKey]);

  // First page on any filter/window change; clears selection (the URL
  // mirror drops the `?log=` param with it).
  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    setSelected(null);
    api
      .searchLogs(windowVal, {
        q: appliedQ || undefined,
        minSeverity: minSeverity || undefined,
        attrs,
        limit: PAGE,
        integration: forcedIntegration,
        service: forcedService,
      })
      .then((r) => {
        if (cancelled) return;
        setLogs(r.logs ?? []);
        setCursor(r.next_cursor);
        setHasMore(!!r.next_cursor);
      })
      .catch((e) => !cancelled && setError(String(e.message ?? e)))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
    };
  }, [windowVal, appliedQ, minSeverity, attrs, forcedIntegration, forcedService, reloadKey]);

  // Deep link: `?log=<LogId>` opens that exact log in the drawer on load,
  // regardless of the current window / filters / keyset page. Fetched by
  // id (org- and policy-scoped server side); a missing or forbidden id is
  // silently ignored so the list still renders. Runs once on mount — the
  // first-page effect above clears `selected` synchronously on each filter
  // change, and this async resolve lands after that, so the two don't race.
  useEffect(() => {
    const logId = searchParams.get("log");
    if (!logId) return;
    let cancelled = false;
    api
      .getLog(logId)
      .then((entry) => {
        if (!cancelled) setSelected(entry);
      })
      .catch(() => {
        /* not found / no access — leave the list as-is */
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const loadMore = useCallback(() => {
    if (!cursor || loadingRef.current) return;
    loadingRef.current = true;
    setLoadingMore(true);
    api
      .searchLogs(windowVal, {
        q: appliedQ || undefined,
        minSeverity: minSeverity || undefined,
        attrs,
        limit: PAGE,
        cursor,
        integration: forcedIntegration,
        service: forcedService,
      })
      .then((r) => {
        setLogs((prev) => [...prev, ...(r.logs ?? [])]);
        setCursor(r.next_cursor);
        setHasMore(!!r.next_cursor);
      })
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => {
        loadingRef.current = false;
        setLoadingMore(false);
      });
  }, [cursor, windowVal, appliedQ, minSeverity, attrs, forcedIntegration, forcedService]);

  const addFilter = (f: LogAttrFilter) => {
    setAttrs((prev) => [...prev.filter((x) => !sameFilter(x, f)), f]);
    setRecent((prev) => {
      const next = [f.key, ...prev.filter((k) => k !== f.key)].slice(0, 8);
      try {
        localStorage.setItem(RECENT_KEY, JSON.stringify(next));
      } catch {
        /* ignore */
      }
      return next;
    });
    setSuggestOpen(false);
  };
  const removeFilter = (i: number) => setAttrs((prev) => prev.filter((_, idx) => idx !== i));

  // The "investigated" chip (accent) is a domain id like order.id, if present.
  const accentIdx = attrs.findIndex((a) => a.key.includes(".id") && !a.key.startsWith("service."));

  // Standalone = the global /logs page (no locked scope). Only there do we
  // render the page header + Refresh; embedded tabs (service/integration)
  // get their own header from the parent and skip this.
  const standalone = !forcedIntegration && !forcedIntegrationId && !forcedService;

  return (
    <div>
      {standalone && (
        <div className="page__header" style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 16, flexWrap: "wrap" }}>
          <div>
            <h1 className="page__title">Logs</h1>
            <p className="page__subtitle">
              Search and inspect log events across services · backed by OpenTelemetry log signal.
            </p>
          </div>
          <button className="btn" onClick={() => setReloadKey((k) => k + 1)} disabled={loading}>
            {loading ? "Loading…" : "Refresh"}
          </button>
        </div>
      )}
      {/* Filter bar — overflow visible so the attribute popover can
          escape the card (cards clip by default for rounded corners). */}
      <div className="card" style={{ padding: "10px 12px", overflow: "visible" }}>
        <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
          {forcedIntegration &&
            // The locked integration scope, non-removable. Clickable through
            // to the integration when we know its id.
            (forcedIntegrationId ? (
              <Link
                to={`/integrations/${forcedIntegrationId}`}
                className="mono"
                style={SCOPE_CHIP_STYLE}
                title="Open this integration"
              >
                integration = {forcedIntegration}
              </Link>
            ) : (
              <span
                className="mono"
                style={SCOPE_CHIP_STYLE}
                title="Scoped to this integration. Policies may further narrow which services you see."
              >
                integration = {forcedIntegration}
              </span>
            ))}
          {forcedService && (
            // Locked service scope (ServiceDetail logs tab), links to the service.
            <Link
              to={`/services/${encodeURIComponent(forcedService)}`}
              className="mono"
              style={SCOPE_CHIP_STYLE}
              title="Open this service"
            >
              service = {forcedService}
            </Link>
          )}
          <div style={{ position: "relative", flex: 1, minWidth: 320, display: "flex", alignItems: "center" }}>
            <span aria-hidden style={{ position: "absolute", left: 10, color: "var(--muted)" }}>⌕</span>
            <input
              className="search__input mono"
              style={{ paddingLeft: 30, fontSize: 13 }}
              placeholder="Filter logs by message text…"
              value={qInput}
              onChange={(e) => setQInput(e.target.value)}
            />
          </div>
          <LevelToggle value={minSeverity} onChange={setMinSeverity} />
          <GroupByControl value={group} onChange={setGroup} dims={logGroupDims} attrKeys={attrKeys} />
          {canWrite && (
            <button
              type="button"
              className="btn"
              style={{ marginLeft: "auto" }}
              title="Create a log alert rule from the current filters"
              onClick={() => setAlertOpen(true)}
            >
              + Create alert rule
            </button>
          )}
        </div>

        <div style={{ display: "flex", gap: 6, alignItems: "center", flexWrap: "wrap", marginTop: 8 }}>
          {attrs.map((a, i) => (
            <FilterChip
              key={`${a.key}-${a.op}-${i}`}
              k={a.key}
              op={a.op}
              value={a.value}
              accent={i === accentIdx}
              onRemove={() => removeFilter(i)}
            />
          ))}
          <div style={{ position: "relative" }}>
            <button
              type="button"
              className="addfilter"
              aria-expanded={suggestOpen}
              onClick={() => setSuggestOpen((o) => !o)}
            >
              + Add attribute filter
            </button>
            {suggestOpen && (
              <AttributeSuggest
                fields={fields}
                recent={recent}
                window={windowVal}
                onPick={addFilter}
                onClose={() => setSuggestOpen(false)}
              />
            )}
          </div>
        </div>
      </div>

      {/* Volume histogram */}
      <div style={{ marginTop: 12 }}>
        <VolumeHistogram
          data={volume}
          loading={volumeLoading}
          onBrush={(from, to) => setWindow(`${from}/${to}`)}
        />
      </div>

      {error && (
        <div className="alert alert--error" style={{ marginTop: 12 }}>
          Failed to load logs: {error}
        </div>
      )}

      {/* Table + drawer split */}
      <div className={`logs-split ${selected ? "is-open" : ""}`} style={{ marginTop: 12 }}>
        <div style={{ minWidth: 0 }}>
          <div className="card" style={{ padding: grouped ? 10 : 0 }}>
            {grouped ? (
              <GroupRollup<LogGroup, LogEntry>
                groups={groups}
                loading={groupsLoading}
                emptyLabel="No log groups in this window."
                cacheKey={groupItemsCacheKey}
                groupKey={(g) => g.key}
                renderLabel={(g) => g.key}
                renderStats={(g) => (
                  <>
                    <span><span className="n">{g.count.toLocaleString()}</span> logs</span>
                    {g.error_count > 0 && (
                      <span className="err"><span className="n">{g.error_count.toLocaleString()}</span> errors</span>
                    )}
                  </>
                )}
                loadItems={loadGroupLogs}
                renderItems={(items) =>
                  items.length === 0 ? (
                    <div className="placeholder" style={{ margin: 10 }}>No logs.</div>
                  ) : (
                    <div>
                      {items.map((l, i) => (
                        <div
                          key={i}
                          className="grp-logrow"
                          style={{ cursor: "pointer" }}
                          onClick={() => setSelected(l)}
                        >
                          <span className="t" title={l.timestamp}>{fmtTime(l.timestamp)}</span>
                          <span><LevelBadge num={l.severity_number} /></span>
                          <span className="svc" title={l.service_name}>{l.service_name || "—"}</span>
                          <span className="msg" title={l.body}>{l.body || "—"}</span>
                        </div>
                      ))}
                    </div>
                  )
                }
              />
            ) : group.by === "attribute" && !group.key ? (
              <div className="placeholder" style={{ margin: 12 }}>Choose an attribute to group by.</div>
            ) : loading && logs.length === 0 ? (
              <div className="placeholder" style={{ margin: 12 }}>Loading…</div>
            ) : (
              <VirtualInfiniteList<LogEntry>
                items={logs}
                hasMore={hasMore}
                loadingMore={loadingMore}
                loadMore={loadMore}
                gridTemplate={GRID}
                rowHeight={40}
                height={listHeight}
                itemKey={(_, i) => String(i)}
                rowClassName={(l) => {
                  const band = severityBand(l.severity_number);
                  const tint = band === "fatal" ? "logrow--fatal" : band === "err" ? "logrow--err" : "";
                  return `logrow ${tint} ${selected === l ? "is-selected" : ""}`;
                }}
                onRowClick={(l) => setSelected(selected === l ? null : l)}
                empty={
                  <div className="placeholder" style={{ margin: 12 }}>
                    No log events match your filters in this window.
                  </div>
                }
                header={
                  <>
                    <span>Time</span>
                    <span>Level</span>
                    <span>Service</span>
                    <span>Message</span>
                    <span>Attributes</span>
                  </>
                }
                renderRow={(l) => {
                  const attrEntries = Object.entries(l.attributes ?? {}).filter(
                    ([k]) => k !== "service.name",
                  );
                  const shown = attrEntries.slice(0, 3);
                  const extra = attrEntries.length - shown.length;
                  return (
                    <>
                      <span className="mono" style={{ fontSize: 12, color: "var(--muted)", whiteSpace: "nowrap" }} title={l.timestamp}>
                        {fmtTime(l.timestamp)}
                      </span>
                      <span><LevelBadge num={l.severity_number} /></span>
                      <span style={{ minWidth: 0, display: "flex", flexDirection: "column", alignItems: "flex-start", justifyContent: "center", gap: 1, overflow: "hidden" }}>
                        {l.service_name ? (
                          <Link
                            className="svc-chip"
                            to={`/services/${encodeURIComponent(l.service_name)}`}
                            title={`Open ${l.service_name}`}
                            onClick={(e) => e.stopPropagation()}
                          >
                            {l.service_name}
                          </Link>
                        ) : (
                          <span className="svc-chip">—</span>
                        )}
                        {(() => {
                          const ints = l.service_name ? svcIntegrations.get(l.service_name) : undefined;
                          if (!ints || ints.length === 0) return null;
                          const first = ints[0];
                          return (
                            <Link
                              to={`/integrations/${first.id}`}
                              className="log-intg-link"
                              title={ints.length > 1 ? `In integrations: ${ints.map((i) => i.name).join(", ")}` : `In integration ${first.name}`}
                              onClick={(e) => e.stopPropagation()}
                            >
                              ↳ {first.name}{ints.length > 1 ? ` +${ints.length - 1}` : ""}
                            </Link>
                          );
                        })()}
                      </span>
                      <span style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0 }}>
                        <span className="mono" style={{ ...ellipsis, fontSize: 12.5 }} title={l.body}>
                          {l.body || "—"}
                        </span>
                        {l.trace_id && (
                          <button
                            type="button"
                            className="trace-pill"
                            title="View this trace"
                            onClick={(e) => {
                              e.stopPropagation();
                              setOpenTraceId(l.trace_id!);
                            }}
                          >
                            ⟿ {l.trace_id.slice(0, 8)}
                          </button>
                        )}
                      </span>
                      <span style={{ display: "flex", gap: 4, minWidth: 0, overflow: "hidden" }}>
                        {shown.map(([k, v]) => (
                          <span className="kvchip" key={k}>
                            <span className="k">{k}=</span>
                            <span className="v">{v}</span>
                          </span>
                        ))}
                        {extra > 0 && <span className="kvchip"><span className="k">+{extra}</span></span>}
                      </span>
                    </>
                  );
                }}
              />
            )}
          </div>
        </div>

        {selected && (
          // No fixed height: the drawer sizes to its content so the grid
          // row grows with it. That gives the sticky left column (the log
          // list) room to stay pinned as the longer drawer scrolls past —
          // a fixed height here capped the row and broke the stickiness.
          <div style={{ minWidth: 0 }}>
            <LogDetailsDrawer
              log={selected}
              integrations={selected.service_name ? svcIntegrations.get(selected.service_name) ?? [] : []}
              onClose={() => setSelected(null)}
              onOpenTrace={setOpenTraceId}
            />
          </div>
        )}
      </div>

      <TraceDrawer
        traceId={openTraceId}
        onClose={() => setOpenTraceId(null)}
        integrationContextId={forcedIntegrationId}
      />

      {alertOpen && (
        <CreateLogAlertDialog
          minSeverity={minSeverity}
          bodyContains={appliedQ}
          attrs={attrs}
          forcedIntegration={forcedIntegration}
          forcedIntegrationId={forcedIntegrationId}
          forcedService={forcedService}
          onClose={() => setAlertOpen(false)}
        />
      )}
    </div>
  );
}

// CreateLogAlertDialog turns the current Logs filters into a log alert
// rule: the match criteria (min severity, body text, attribute filters)
// are captured from the page; the operator adds a threshold, window,
// severity, optional service health-binding, and notification channels.
function CreateLogAlertDialog({
  minSeverity,
  bodyContains,
  attrs,
  forcedIntegration,
  forcedIntegrationId,
  forcedService,
  onClose,
}: {
  minSeverity: number;
  bodyContains: string;
  attrs: LogAttrFilter[];
  forcedIntegration?: string;
  forcedIntegrationId?: string;
  forcedService?: string;
  onClose: () => void;
}) {
  const svcAttr = attrs.find((a) => a.key === "service.name" && a.op === "eq");
  const [name, setName] = useState("");
  const [severity, setSeverity] = useState<AlertSeverity>("warning");
  const [threshold, setThreshold] = useState(1);
  const [windowN, setWindowN] = useState(5);
  const [windowUnit, setWindowUnit] = useState<"seconds" | "minutes" | "hours">("minutes");
  const [serviceName, setServiceName] = useState(svcAttr?.value ?? forcedService ?? "");
  const [allServices, setAllServices] = useState<string[]>([]);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [channelIDs, setChannelIDs] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.listChannels().then((r) => setChannels(r.channels ?? [])).catch(() => setChannels([]));
    // Every service in the catalog — bind this log rule to any of them as
    // a health check, not just the one the logs are currently scoped to.
    api
      .listServices("24h")
      .then((r) => setAllServices((r.services ?? []).map((s) => s.service_name).sort()))
      .catch(() => setAllServices([]));
  }, []);

  const windowSeconds =
    windowUnit === "hours" ? windowN * 3600 : windowUnit === "minutes" ? windowN * 60 : windowN;

  // Attribute filters for the rule = the page's attrs, minus a
  // service.name=eq when we're binding it as the health service (avoids
  // a redundant predicate; the service binding already scopes the count).
  const specAttrs = attrs
    .filter((a) => !(a.key === "service.name" && a.op === "eq" && a.value === serviceName.trim()))
    .map((a) => ({ key: a.key, op: a.op, value: a.value }));

  const submit = async () => {
    setBusy(true);
    setError(null);
    const body: AlertRuleInput = {
      name: name.trim(),
      severity,
      enabled: true,
      signal: "log",
      log_spec: {
        min_severity: minSeverity,
        body_contains: bodyContains,
        attrs: specAttrs,
        threshold: Math.max(1, threshold),
        window_seconds: Math.max(60, windowSeconds),
      },
      channel_ids: channelIDs,
      service_name: serviceName.trim() || undefined,
      integration_id: forcedIntegrationId || undefined,
    };
    try {
      await api.createAlertRule(body);
      onClose();
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setBusy(false);
    }
  };

  const toggleChannel = (id: string) =>
    setChannelIDs((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));

  const sevLabel =
    minSeverity >= 21 ? "fatal" : minSeverity >= 17 ? "error" : minSeverity >= 13 ? "warn" : minSeverity > 0 ? `≥${minSeverity}` : "any";
  const criteria = [
    `severity: ${sevLabel}`,
    bodyContains ? `contains "${bodyContains}"` : null,
    ...specAttrs.map((a) => `${a.key} ${a.op} ${a.value}`),
  ].filter(Boolean);

  return (
    <EditDrawer title="New log alert rule" width="wide" onClose={onClose}>
      <div className="form" style={{ display: "flex", flexDirection: "column", gap: 14 }}>
        <div className="muted" style={{ fontSize: 12.5 }}>
          Fires when matching logs reach the threshold in the window. Captured from your current
          filters:
          <div style={{ display: "flex", gap: 6, flexWrap: "wrap", marginTop: 6 }}>
            {criteria.map((c, i) => (
              <span key={i} className="badge mono" style={{ fontSize: 11 }}>{c}</span>
            ))}
            {forcedIntegration && (
              <span className="badge mono" style={{ fontSize: 11 }}>integration: {forcedIntegration}</span>
            )}
            {forcedService && (
              <span className="badge mono" style={{ fontSize: 11 }}>service: {forcedService}</span>
            )}
          </div>
        </div>

        <label className="form__label">
          Name
          <input className="search__input" value={name} onChange={(e) => setName(e.target.value)}
            placeholder="Order errors" autoFocus />
        </label>

        <div style={{ display: "flex", gap: 10, flexWrap: "wrap", alignItems: "flex-end" }}>
          <label className="form__label" style={{ minWidth: 120 }}>
            Severity
            <select className="toolbar__select" value={severity}
              onChange={(e) => setSeverity(e.target.value as AlertSeverity)}>
              <option value="info">info</option>
              <option value="warning">warning</option>
              <option value="critical">critical</option>
            </select>
          </label>
          <label className="form__label" style={{ minWidth: 110 }}>
            Fire at ≥
            <input type="number" min={1} className="search__input mono" value={threshold}
              onChange={(e) => setThreshold(Math.max(1, parseInt(e.target.value, 10) || 1))} />
          </label>
          <label className="form__label">
            Within
            <div style={{ display: "flex", gap: 6 }}>
              <input type="number" min={1} className="search__input mono" style={{ width: 80 }}
                value={windowN} onChange={(e) => setWindowN(Math.max(1, parseInt(e.target.value, 10) || 1))} />
              <select className="toolbar__select" value={windowUnit}
                onChange={(e) => setWindowUnit(e.target.value as "seconds" | "minutes" | "hours")}>
                <option value="seconds">seconds</option>
                <option value="minutes">minutes</option>
                <option value="hours">hours</option>
              </select>
            </div>
          </label>
        </div>

        <label className="form__label">
          ♥ Add as a health check to a service (optional)
          <SearchableSelect
            value={serviceName}
            onChange={setServiceName}
            options={allServices}
            allLabel="Don't — just alert, leave service health unchanged"
            placeholder="Pick the service this rule reflects…"
          />
          <span className="form__hint">
            {serviceName
              ? `${serviceName} (and any integration it belongs to) reads "unhealthy" while this rule is firing — a log-based health check.`
              : "Optional — bind this rule to a service so it (and its integrations) reads unhealthy whenever it fires."}
          </span>
        </label>

        <div className="form__label">
          <span>Alert channels</span>
          {channels.length === 0 ? (
            <p className="muted" style={{ fontSize: 12 }}>
              No channels configured — add one on the <Link to="/alerts">Alerts</Link> page.
            </p>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
              {channels.map((c) => (
                <label key={c.id} style={{ display: "flex", gap: 8, alignItems: "center" }}>
                  <input type="checkbox" checked={channelIDs.includes(c.id)} onChange={() => toggleChannel(c.id)} />
                  <span><b>{c.name}</b> <span className="muted">· {c.kind}</span></span>
                </label>
              ))}
            </div>
          )}
        </div>

        {error && <div className="alert alert--error">{error}</div>}

        <div className="form__actions">
          <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
          <button type="button" className="btn btn--primary" onClick={submit} disabled={busy || !name.trim()}>
            {busy ? "Creating…" : "Create rule"}
          </button>
        </div>
      </div>
    </EditDrawer>
  );
}
