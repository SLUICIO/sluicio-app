// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Service detail page (Sluicio Service design handoff): a viewer with a
// hero + health pill, golden signals, the service's health checks,
// trace-derived dependencies, and a metadata / activity / notifications
// rail — plus an edit mode for the editable Identity metadata, health
// checks, notification routing, and the advanced (facets / custom
// metrics) config. Tabs surface the deeper Metrics / Logs / Traces views.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api } from "../api/client";
import AddToIntegration from "../components/AddToIntegration";
import PublicBadgeControl from "../components/PublicBadgeControl";
import ServiceReadingTiles from "../components/health/ServiceReadingTiles";
import HealthChecks, { HealthCheckEditDrawer } from "../components/health/HealthChecks";
import HealthCheckResultDrawer from "../components/health/HealthCheckResultDrawer";
import FacetMappingsEditor from "../components/FacetMappingsEditor";
import UserMetadataPanel from "../components/MetadataPanel";
import ServiceFacetsEditor from "../components/ServiceFacetsEditor";
import ServiceSchemasPanel from "../components/ServiceSchemasPanel";
import LogsView from "../components/logs/LogsView";
import MetricsExplorer from "../components/metrics/MetricsExplorer";
import SearchableSelect from "../components/SearchableSelect";
import ServiceMessagesTab from "../components/ServiceMessagesTab";
import ServiceFlowGraph from "../components/ServiceFlowGraph";
import CreateTraceAlertDrawer from "../components/CreateTraceAlertDrawer";
import TagPicker from "../components/tags/TagPicker";
import Sparkline from "../components/metrics/Sparkline";
import type {
  AlertInstance,
  AlertRule,
  CreateTagRequest,
  FlowEdge,
  FlowNode,
  MonitoringTemplate,
  NeighborsResponse,
  NotificationChannel,
  ServiceDetailResponse,
  ServiceMetadata,
  ServiceReading,
  ServiceStatus,
  SystemType,
  Tag,
} from "../api/types";
import { alertCondition, alertSignalLabel } from "../lib/alertRule";
import { SERVICE_TEMPLATE_KINDS, hasSystemTemplate, systemKindLabel, templateKindLabel } from "../lib/systemKinds";
import { formatDurationMs, formatNumber, formatRelative } from "../lib/format";
import { useBreadcrumbLeaf } from "../lib/breadcrumb";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";
import { useCurrentUser } from "../lib/useCurrentUser";
import { useAccess } from "../lib/useAccess";

const CHANNEL_GLYPH: Record<string, string> = { slack: "#", pagerduty: "⚠", webhook: "↗" };
// Window used when jumping to the error traces behind an open-error
// (unacknowledged) health failure — matches the backend's 30-day
// openErrorLookback so those traces are guaranteed to be in range.
const OPEN_ERROR_TRACE_WINDOW = "30d";
type Mode = "view" | "edit";
type ViewTab = "overview" | "health" | "metrics" | "logs" | "traces" | "deploys" | "settings";

export default function ServiceDetail() {
  const { name = "" } = useParams();
  const navigate = useNavigate();
  const [windowVal, setWindow] = useTimeWindow();
  const { can } = useCurrentUser();
  const access = useAccess();
  // Scoped manage (RBAC v2): org editors, or group-editors whose scope
  // covers this service.
  const canWrite = can("integration.write") || access.canManageService(name);

  const [mode, setMode] = useState<Mode>("view");
  // Failed-trace alert drawer (opened from the Traces tab).
  const [showTraceAlert, setShowTraceAlert] = useState(false);
  // Health-check detail blade: the check clicked on the Health tab (a rule,
  // or the built-in error-span pseudo-check). null = blade closed.
  const [checkDetail, setCheckDetail] = useState<{ rule: AlertRule | null; builtin: boolean } | null>(null);
  const [editCheckRule, setEditCheckRule] = useState<AlertRule | null>(null);
  // Deep-linkable tab, hydrated from the URL so a link like
  // /services/x?tab=traces reopens on the right tab. The Traces tab's own
  // filter/search state lives in <ServiceMessagesTab> (?q / ?s).
  const initialParams = useRef(new URLSearchParams(window.location.search)).current;
  const [tab, setTab] = useState<ViewTab>(() => {
    const t = initialParams.get("tab") as ViewTab | null;
    const valid: ViewTab[] = ["overview", "health", "metrics", "logs", "traces", "settings"];
    return t && valid.includes(t) ? t : "overview";
  });

  const [data, setData] = useState<ServiceDetailResponse | null>(null);
  const [meta, setMeta] = useState<ServiceMetadata | null>(null);
  const [neighbors, setNeighbors] = useState<NeighborsResponse | null>(null);
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [instances, setInstances] = useState<AlertInstance[]>([]);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  const [allTags, setAllTags] = useState<Tag[]>([]);
  const [tagsSaving, setTagsSaving] = useState(false);

  // Detected monitoring-template kinds for this service (from emitted
  // metrics) — surfaced as a prominent banner so the detection is obvious,
  // dismissible per service.
  const [tmplSuggestions, setTmplSuggestions] = useState<
    { kind: string; label: string; system: boolean; check_count: number; applied: boolean }[]
  >([]);
  const [tmplHintDismissed, setTmplHintDismissed] = useState(false);
  // Dismissal of the "Errors cleared" banner, persisted by the ack's
  // timestamp so closing it sticks across reloads — but a fresh clear
  // (new timestamp) shows the banner again.
  const [ackDismissedAt, setAckDismissedAt] = useState<string | null>(null);
  // Only prompt for kinds not yet applied — once a service's checks match the
  // template, the "Detected …" banner is noise.
  const pendingSuggestions = useMemo(() => tmplSuggestions.filter((s) => !s.applied), [tmplSuggestions]);
  useEffect(() => {
    setTmplHintDismissed(window.localStorage.getItem(`im.svc.tmplhint.${name}`) === "1");
    setAckDismissedAt(window.localStorage.getItem(`im.svc.errack.${name}`));
    let alive = true;
    api
      .templateSuggestions(name)
      .then((r) => { if (alive) setTmplSuggestions(r.suggestions ?? []); })
      .catch(() => {});
    return () => { alive = false; };
  }, [name]);
  const dismissTmplHint = () => {
    setTmplHintDismissed(true);
    window.localStorage.setItem(`im.svc.tmplhint.${name}`, "1");
  };
  const dismissAck = (acknowledgedAt: string) => {
    setAckDismissedAt(acknowledgedAt);
    window.localStorage.setItem(`im.svc.errack.${name}`, acknowledgedAt);
  };

  // Golden-signals mode — "trace" (the built-in error/latency/throughput
  // signals) or "metric" (driven by this service's metric-type health checks).
  // Per-user, per-service, persisted in localStorage. Re-read when the route's
  // service changes so each service keeps its own choice.
  const [goldenMode, setGoldenModeState] = useState<GoldenMode>(() => readGoldenMode(name));
  useEffect(() => {
    setGoldenModeState(readGoldenMode(name));
  }, [name]);
  const setGoldenMode = (m: GoldenMode) => {
    setGoldenModeState(m);
    try {
      window.localStorage.setItem(goldenModeKey(name), m);
    } catch {
      /* private mode — falls back to the in-memory choice for this session */
    }
  };

  // "Clear errors" flow: an inline comment box + submit, and undo.
  const [showClear, setShowClear] = useState(false);
  const [clearComment, setClearComment] = useState("");
  const [clearing, setClearing] = useState(false);

  const refresh = useCallback(() => {
    setLoading(true);
    setError(null);
    api
      .serviceDetail(name, windowVal)
      .then((detail) => {
        setData(detail);
      })
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
    api.getServiceMetadata(name).then(setMeta).catch(() => setMeta(null));
    api.serviceNeighbors(name, windowVal).then(setNeighbors).catch(() => setNeighbors(null));
    api.listAlertRules({ service: name }).then((r) => setRules(r.rules ?? [])).catch(() => setRules([]));
    api.listAlertInstances(200).then((r) => setInstances(r.instances ?? [])).catch(() => setInstances([]));
    api.listChannels().then((r) => setChannels(r.channels ?? [])).catch(() => setChannels([]));
    api.listTags().then((d) => setAllTags(d.tags ?? [])).catch(() => setAllTags([]));
  }, [name, windowVal]);

  useEffect(refresh, [refresh]);

  const clearErrors = async () => {
    setClearing(true);
    try {
      await api.clearServiceErrors(name, clearComment.trim() || undefined);
      setShowClear(false);
      setClearComment("");
      refresh();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setClearing(false);
    }
  };
  const undoClear = async () => {
    try {
      await api.unclearServiceErrors(name);
      refresh();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  // Keep the active tab in the URL so it's a shareable deep link
  // (preserves ?range= from the header, and ?q / ?s owned by the Traces
  // tab's <ServiceMessagesTab>).
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (tab === "overview") params.delete("tab");
    else params.set("tab", tab);
    const next = params.toString();
    const target = `${window.location.pathname}${next ? `?${next}` : ""}`;
    if (target !== `${window.location.pathname}${window.location.search}`) {
      window.history.replaceState(null, "", target);
    }
  }, [tab]);

  const ruleIds = useMemo(() => new Set(rules.map((r) => r.id)), [rules]);
  const firingByRule = useMemo(() => {
    const m = new Map<string, AlertInstance>();
    for (const i of instances) {
      if (i.state === "firing" && ruleIds.has(i.alert_rule_id)) m.set(i.alert_rule_id, i);
    }
    return m;
  }, [instances, ruleIds]);
  const failing = useMemo(() => rules.filter((r) => firingByRule.has(r.id)).length, [rules, firingByRule]);

  const applyTagSelection = async (nextIds: string[]) => {
    if (!data) return;
    const currentIds = (data.tags ?? []).map((t) => t.id);
    const toAdd = nextIds.filter((x) => !currentIds.includes(x));
    const toRemove = currentIds.filter((x) => !nextIds.includes(x));
    setTagsSaving(true);
    try {
      await Promise.all([
        ...toAdd.map((tid) => api.attachServiceTag(name, tid)),
        ...toRemove.map((tid) => api.detachServiceTag(name, tid)),
      ]);
      setData({ ...data, tags: allTags.filter((t) => nextIds.includes(t.id)) });
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setTagsSaving(false);
      refresh();
    }
  };
  const createTag = async (req: CreateTagRequest): Promise<Tag> => {
    const created = await api.createTag(req);
    setAllTags((curr) => [...curr, created].sort((a, b) => a.name.localeCompare(b.name)));
    return created;
  };

  const status = data?.status ?? "quiet";
  const pillClass = status === "unhealthy" ? "err" : status === "errors" ? "warn" : "";
  // Why is it unhealthy? A firing health check ("checks") vs persisted
  // unacknowledged trace errors ("errors", failing === 0). Drives both the
  // pill wording and where clicking it takes you. null = nothing to drill into.
  const pillReason: "checks" | "errors" | null =
    status === "unhealthy" ? (failing > 0 ? "checks" : "errors") : status === "errors" ? "errors" : null;
  const pillText =
    status === "unhealthy"
      ? failing > 0
        ? `Unhealthy · ${failing} check${failing === 1 ? "" : "s"} failing`
        : "Unhealthy · unacknowledged error traces"
      : status === "errors"
        ? "Degraded · error traces"
        : status === "quiet"
          ? "Quiet · no recent traffic"
          : "Healthy · all checks passing";

  // Jump to the Traces tab — optionally pre-filtered to error traces (the
  // same ?s shorthand the Messages views use). This is how a trace-based
  // health-check failure links to the actual traces behind it.
  //
  // widen: open (unacknowledged) errors are tracked over a 30-day lookback,
  // independent of the header time window — so jumping with a narrow window
  // (e.g. 30m) shows an empty list even though the service is unhealthy.
  // Widen the window to cover that lookback so the offending traces are
  // actually in range. (Trace-rule checks fire on recent windowed traffic,
  // so they keep the current window.)
  const goToTraces = (errorsOnly: boolean, widen = false) => {
    if (widen) setWindow(OPEN_ERROR_TRACE_WINDOW);
    const params = new URLSearchParams(window.location.search);
    params.set("tab", "traces");
    if (errorsOnly) params.set("s", "err only");
    else params.delete("s");
    window.history.replaceState(null, "", `${window.location.pathname}?${params.toString()}`);
    setTab("traces");
  };
  const goToErrorTraces = () => goToTraces(true, true);

  // Open the service Logs tab pre-filtered to a health check's match (the
  // dedicated ?logmin / ?logq params LogsView hydrates on mount).
  const goToLogs = (minSeverity: number, body: string) => {
    const params = new URLSearchParams(window.location.search);
    params.set("tab", "logs");
    if (minSeverity > 0) params.set("logmin", String(minSeverity));
    else params.delete("logmin");
    if (body) params.set("logq", body);
    else params.delete("logq");
    window.history.replaceState(null, "", `${window.location.pathname}?${params.toString()}`);
    setTab("logs");
    setCheckDetail(null);
  };

  // Jump to the global Metrics explorer focused on the check's metric, and
  // carry the check's attribute scope (e.g. rabbitmq.queue.name=orders) so
  // the explorer opens pre-filtered to the same series, not the whole metric.
  const goToMetrics = (metric: string, attrs?: { key: string; op: string; value: string }[]) => {
    setCheckDetail(null);
    const params = new URLSearchParams();
    params.set("metric", metric);
    if (attrs && attrs.length > 0) params.set("mattr", JSON.stringify(attrs));
    navigate(`/metrics?${params.toString()}`);
  };

  // Clicking the pill drills into what's wrong: failing health checks open
  // the Health checks tab; trace errors open the error-filtered Traces tab.
  const onPillClick = () => {
    if (pillReason === "checks") {
      setTab("health");
      return;
    }
    if (pillReason === "errors") {
      goToErrorTraces();
    }
  };

  // Per-signal visibility (RBAC v2 §7): hide telemetry tabs the caller's
  // policies don't grant for this service. Absent field (older cell or
  // admin wildcard) = show everything.
  const signalVisible = (sig: string) =>
    !data?.visible_signals || data.visible_signals.includes(sig);
  const viewTabs: [ViewTab, string, number?][] = ([
    ["overview", "Overview"],
    ["health", "Health checks", rules.length || undefined],
    ["metrics", "Metrics"],
    ["logs", "Logs"],
    ["traces", "Traces"],
    ["settings", "Settings"],
  ] as [ViewTab, string, number?][]).filter(([t]) =>
    t === "metrics" ? signalVisible("metrics")
    : t === "logs" ? signalVisible("logs")
    : t === "traces" ? signalVisible("traces")
    : true);

  // A 404 means the service doesn't exist OR the caller can't see it. Don't
  // render the page chrome (which would leak the name in the header/title) —
  // show a plain not-found state instead, and keep the name out of the tab
  // title + breadcrumb too (the breadcrumb otherwise falls back to the URL
  // segment, which is the service name).
  const notFound = !data && !!error && /^404\b/.test(error);
  usePageTitle(notFound ? "Not found" : name);
  useBreadcrumbLeaf(notFound ? "Not found" : null);
  if (notFound) {
    return (
      <div className="placeholder" style={{ marginTop: 64, textAlign: "center" }}>
        <h2 style={{ margin: 0 }}>Service not found</h2>
        <p className="muted" style={{ marginTop: 8 }}>
          It doesn't exist, or you don't have access to it.
        </p>
        <Link to="/services" className="btn" style={{ marginTop: 16, display: "inline-block" }}>
          ← All services
        </Link>
      </div>
    );
  }

  return (
    <div>
      {/* hero */}
      <div className="svc-page-head">
        <div className="svc-id">
          <div className="svc-crumb">
            <Link to="/services">Services</Link><span>›</span><b className="mono">{name}</b>
          </div>
          <div className="svc-title-row">
            <h1 className="svc-title">{name}</h1>
            {pillReason ? (
              <button
                type="button"
                className={`svc-health-pill ${pillClass} svc-health-pill--btn`}
                onClick={onPillClick}
                title={pillReason === "checks" ? "See the failing health checks" : "See the failing traces"}
              >
                <span className="pip" />{pillText}<span className="svc-health-pill__chev" aria-hidden>›</span>
              </button>
            ) : (
              <span className={`svc-health-pill ${pillClass}`}><span className="pip" />{pillText}</span>
            )}
          </div>
          {meta?.description && <div className="svc-desc">{meta.description}</div>}
          <div className="svc-meta-row">
            {data?.service_namespace && <Meta k="namespace" v={data.service_namespace} mono />}
            {meta?.team && <Meta k="team" v={`team:${meta.team}`} mono />}
            {meta?.owner && <Meta k="owner" v={meta.owner} />}
            {meta?.on_call && <Meta k="on-call" v={meta.on_call} />}
            <Meta k="traces" v={formatNumber(data?.stats.trace_count ?? 0)} mono />
            <Meta k="p95" v={formatDurationMs(data?.stats.p95_duration_ms ?? 0)} mono />
          </div>
        </div>
        <div className="svc-head-actions">
          {meta?.runbook_url && (
            <a className="btn ghost" href={meta.runbook_url} target="_blank" rel="noreferrer">Runbook ↗</a>
          )}
          <button className="btn ghost" onClick={refresh}>↻ Re-evaluate</button>
          {canWrite && (data?.stats.error_trace_count ?? 0) > 0 && !data?.error_ack && !showClear && (
            <button
              className="btn ghost"
              title="Mark current errors as reviewed — health resets until new failures arrive"
              onClick={() => setShowClear(true)}
            >
              ✓ Clear errors
            </button>
          )}
          {/* Edit is editor+ only — viewers are read-only. */}
          {canWrite &&
            (mode === "view" ? (
              <button className="btn primary" onClick={() => setMode("edit")}>✎ Edit service</button>
            ) : (
              <button className="btn ghost" onClick={() => { setMode("view"); refresh(); }}>← Back to viewer</button>
            ))}
        </div>
      </div>

      {error && <div className="alert alert--error" style={{ margin: "12px 0" }}>Failed to load service: {error}</div>}

      {/* Inline "clear errors" comment form */}
      {showClear && (
        <div className="alert alert--info" style={{ margin: "12px 0", flexDirection: "column", alignItems: "stretch", gap: 8 }}>
          <strong>Clear current errors for {name}?</strong>
          <span className="muted" style={{ fontSize: 12.5 }}>
            Marks the {formatNumber(data?.stats.error_trace_count ?? 0)} error trace
            {(data?.stats.error_trace_count ?? 0) === 1 ? "" : "s"} as reviewed. The service
            reads healthy again until new failures arrive; the traces themselves stay in history.
          </span>
          <input
            className="search__input"
            placeholder="Optional comment (e.g. 'known issue, fix deployed in #1234')"
            value={clearComment}
            onChange={(e) => setClearComment(e.target.value)}
            style={{ fontSize: 13 }}
          />
          <div style={{ display: "flex", gap: 8 }}>
            <button className="btn primary" disabled={clearing} onClick={clearErrors}>
              {clearing ? "Clearing…" : "Clear errors"}
            </button>
            <button className="btn ghost" disabled={clearing} onClick={() => { setShowClear(false); setClearComment(""); }}>
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Active acknowledgement banner — dismissible (closing it sticks per
          ack; a fresh clear shows it again). */}
      {data?.error_ack && !showClear && ackDismissedAt !== data.error_ack.acknowledged_at && (
        <div className="alert alert--info" style={{ margin: "12px 0", display: "flex", alignItems: "flex-start", gap: 10 }}>
          <span style={{ flex: 1 }}>
            <strong>Errors cleared</strong>
            {data.error_ack.acknowledged_by_name ? ` by ${data.error_ack.acknowledged_by_name}` : ""}{" "}
            {formatRelative(data.error_ack.acknowledged_at)}
            {data.error_ack.comment ? ` — ${data.error_ack.comment}` : ""}. New errors since then{" "}
            {(data.stats.error_trace_count ?? 0) > 0
              ? `(${formatNumber(data.stats.error_trace_count)}) are counted again.`
              : "will show here."}{" "}
            <button className="btn--link" style={{ padding: 0 }} onClick={undoClear}>Undo</button>
          </span>
          <button
            type="button"
            aria-label="Dismiss this notice"
            title="Dismiss"
            onClick={() => dismissAck(data!.error_ack!.acknowledged_at)}
            style={{ border: 0, background: "transparent", cursor: "pointer", color: "inherit", fontSize: 14, lineHeight: 1, opacity: 0.6, padding: 2 }}
          >
            ✕
          </button>
        </div>
      )}

      {loading && !data && <div className="placeholder" style={{ marginTop: 12 }}>Loading…</div>}

      {data && mode === "view" && (
        <>
          {pendingSuggestions.length > 0 && !tmplHintDismissed && (
            <div
              style={{
                display: "flex",
                alignItems: "center",
                gap: 12,
                margin: "0 0 16px",
                padding: "12px 16px",
                borderRadius: 10,
                background: "var(--primary-soft)",
                border: "1px solid color-mix(in oklab, var(--primary) 35%, transparent)",
                borderLeft: "4px solid var(--primary)",
                color: "var(--primary-ink)",
              }}
            >
              <span style={{ fontSize: 18 }}>⚙</span>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontWeight: 600 }}>
                  Detected {pendingSuggestions.map((s) => s.label).join(", ")}
                </div>
                <div style={{ fontSize: 13, opacity: 0.9 }}>
                  Sluicio can set up starter health checks for this service from its emitted metrics.
                </div>
              </div>
              {canWrite && (
                <button className="btn primary" onClick={() => setMode("edit")}>
                  Set up monitoring
                </button>
              )}
              <button
                className="btn ghost"
                aria-label="Dismiss"
                title="Dismiss"
                onClick={dismissTmplHint}
                style={{ padding: "4px 8px" }}
              >
                ✕
              </button>
            </div>
          )}

          <div className="svc-tabs">
            {viewTabs.map(([id, label, count]) => (
              <button key={id} className={`svc-tab ${tab === id ? "on" : ""}`} onClick={() => setTab(id)}>
                {label}
                {count != null && <span className="count">{count}</span>}
              </button>
            ))}
          </div>

          <div className="svc-body">
            {tab === "overview" && (
              <div className="svc-grid">
                <div style={{ display: "flex", flexDirection: "column", gap: 20 }}>
                  <GoldenSignals data={data} serviceName={name} mode={goldenMode} onModeChange={setGoldenMode} />
                  <HealthChecksView rules={rules} firingByRule={firingByRule} failing={failing} openErrorCount={data?.open_error_count ?? 0} onEdit={() => setMode("edit")} onSelectCheck={setCheckDetail} window={windowVal} />
                  <Dependencies neighbors={neighbors} name={name} ownTraceCount={data?.stats.trace_count ?? 0} ownStatus={data?.status} />
                </div>
                <div style={{ display: "flex", flexDirection: "column", gap: 20 }}>
                  <MetadataPanel name={name} data={data} meta={meta} onChanged={refresh} />
                  <UserMetadataPanel
                    fields={meta?.metadata_fields ?? []}
                    values={meta?.metadata_values ?? {}}
                    onSave={async (next) => {
                      await api.setServiceMetadataExtras(name, next);
                      const fresh = await api.getServiceMetadata(name);
                      setMeta(fresh);
                    }}
                    title="Custom metadata"
                  />
                  <ServiceSchemasPanel
                    in={meta?.in_schema}
                    out={meta?.out_schema}
                    onSave={async (next) => {
                      await api.setServiceSchemas(name, next);
                      const fresh = await api.getServiceMetadata(name);
                      setMeta(fresh);
                    }}
                  />
                  <ActivityPanel instances={instances} ruleIds={ruleIds} />
                  <NotificationsPanel rules={rules} channels={channels} />
                </div>
              </div>
            )}

            {tab === "health" && (
              <HealthChecksView rules={rules} firingByRule={firingByRule} failing={failing} openErrorCount={data?.open_error_count ?? 0} onEdit={() => setMode("edit")} onSelectCheck={setCheckDetail} window={windowVal} wide />
            )}

            {tab === "metrics" && <MetricsExplorer service={name} embedded />}

            {tab === "logs" && <LogsView forcedService={name} />}

            {tab === "traces" && (
              <>
                {canWrite && (
                  <div style={{ display: "flex", justifyContent: "flex-end", marginBottom: 8 }}>
                    <button
                      type="button"
                      className="btn"
                      title="Alert when this service accumulates failed traces"
                      onClick={() => setShowTraceAlert(true)}
                    >
                      + Alert on failed traces
                    </button>
                  </div>
                )}
                <ServiceMessagesTab serviceName={name} />
                {showTraceAlert && (
                  <CreateTraceAlertDrawer serviceName={name} onClose={() => setShowTraceAlert(false)} />
                )}
              </>
            )}

            {tab === "settings" && (
              <div style={{ maxWidth: 640 }}>
                <div className="svc-section">
                  <div className="svc-section-head"><span className="svc-section-title">Public status badge</span></div>
                  <div style={{ padding: "12px 16px" }}>
                    <PublicBadgeControl
                      kind="service"
                      id={name}
                      enabled={data.badge_public ?? false}
                      canManage={canWrite}
                      onChange={refresh}
                    />
                  </div>
                </div>
              </div>
            )}
          </div>
        </>
      )}

      {data && mode === "edit" && (
        <ServiceEdit
          name={name}
          data={data}
          meta={meta}
          allTags={allTags}
          tagsSaving={tagsSaving}
          rules={rules}
          channels={channels}
          windowVal={windowVal}
          onTagChange={applyTagSelection}
          onCreateTag={createTag}
          onChanged={refresh}
        />
      )}

      {/* Health-check detail blade — opened by clicking a check on the
          Health tab; shows the live evidence + a filtered deep link. */}
      {checkDetail && (
        <HealthCheckResultDrawer
          rule={checkDetail.rule}
          builtin={checkDetail.builtin}
          serviceName={name}
          window={windowVal}
          firing={checkDetail.builtin ? (data?.open_error_count ?? 0) > 0 : !!(checkDetail.rule && firingByRule.has(checkDetail.rule.id))}
          openErrorCount={data?.open_error_count ?? 0}
          onClose={() => setCheckDetail(null)}
          canEdit={canWrite}
          onEdit={() => { const r = checkDetail?.rule ?? null; setCheckDetail(null); setEditCheckRule(r); }}
          onOpenLogs={goToLogs}
          onOpenMetrics={goToMetrics}
          onOpenTraces={goToTraces}
        />
      )}

      {/* Edit this health check — the same blade + config as editing it from
          the checks list, opened without leaving the Health tab. */}
      {editCheckRule && (
        <HealthCheckEditDrawer
          rule={editCheckRule}
          scope="service"
          target={name}
          window={windowVal}
          onSaved={() => { setEditCheckRule(null); refresh(); }}
          onClose={() => setEditCheckRule(null)}
        />
      )}
    </div>
  );
}

function Meta({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <span className="mi">
      <span className="k">{k}</span>
      <span className={`v ${mono ? "mono" : ""}`}>{v}</span>
    </span>
  );
}

// ── golden signals ─────────────────────────────────────────────
// Per-service preference: the built-in trace signals, or tiles driven by the
// service's metric-type health checks (its "show on service page" readings).
type GoldenMode = "trace" | "metric";
const goldenModeKey = (svc: string) => `im.svc.goldenmode.${svc}`;
function readGoldenMode(svc: string): GoldenMode {
  try {
    return window.localStorage.getItem(goldenModeKey(svc)) === "metric" ? "metric" : "trace";
  } catch {
    return "trace";
  }
}
const OP_GLYPH: Record<string, string> = { gt: ">", gte: "≥", lt: "<", lte: "≤", eq: "=", neq: "≠" };

function GoldenSignals({
  data,
  serviceName,
  mode,
  onModeChange,
}: {
  data: ServiceDetailResponse;
  serviceName: string;
  mode: GoldenMode;
  onModeChange: (m: GoldenMode) => void;
}) {
  const s = data.stats;
  // Real bucketed series from the backend (traces / error rate / p50 /
  // p95 per bucket). Absent series → empty array → flat sparkline.
  const ser = data.stats_series;
  const traceSignals = [
    { k: "Traces", v: formatNumber(s.trace_count), u: "in window", spark: ser?.traces ?? [] },
    { k: "Error rate", v: (s.error_rate * 100).toFixed(2), u: "%", breach: s.error_rate > 0.01, spark: (ser?.error_rate ?? []).map((r) => r * 100) },
    { k: "p95 latency", v: formatDurationMs(s.p95_duration_ms), u: "", spark: ser?.p95_ms ?? [] },
    { k: "p50 latency", v: formatDurationMs(s.p50_duration_ms), u: "", spark: ser?.p50_ms ?? [] },
  ];

  // Metric-check readings — only fetched when the metric view is active.
  const [readings, setReadings] = useState<ServiceReading[]>([]);
  const [readingsLoaded, setReadingsLoaded] = useState(false);
  useEffect(() => {
    if (mode !== "metric") return;
    let alive = true;
    setReadingsLoaded(false);
    api
      .serviceReadings(serviceName)
      .then((r) => { if (alive) setReadings(r.readings ?? []); })
      .catch(() => { if (alive) setReadings([]); })
      .finally(() => { if (alive) setReadingsLoaded(true); });
    return () => { alive = false; };
  }, [mode, serviceName]);

  return (
    <div className="svc-section">
      <div className="svc-section-head">
        <span className="svc-section-title">Golden signals</span>
        <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
          <div role="tablist" aria-label="Golden signals source" style={{ display: "inline-flex", border: "1px solid var(--border)", borderRadius: 6, overflow: "hidden" }}>
            {(["trace", "metric"] as GoldenMode[]).map((m) => (
              <button
                key={m}
                type="button"
                role="tab"
                aria-selected={mode === m}
                onClick={() => onModeChange(m)}
                style={{
                  border: 0,
                  padding: "3px 10px",
                  fontSize: 12,
                  cursor: "pointer",
                  background: mode === m ? "var(--primary-soft)" : "transparent",
                  color: mode === m ? "var(--primary-ink)" : "var(--ink-2)",
                  fontWeight: mode === m ? 600 : 400,
                }}
              >
                {m === "trace" ? "Traces" : "Metric checks"}
              </button>
            ))}
          </div>
          <span className="svc-section-sub">
            {mode === "trace" ? "Per-bucket · selected window" : "Latest metric-check readings"}
          </span>
        </div>
      </div>

      {mode === "trace" ? (
        <div className="svc-signals">
          {traceSignals.map((sig, i) => (
            <div key={i} className="svc-signal">
              <span className="svc-signal-k">{sig.k}</span>
              <div className="svc-signal-row">
                <span className={`svc-signal-v ${sig.breach ? "br" : ""}`}>{sig.v}</span>
                {sig.u && <span className="svc-signal-u">{sig.u}</span>}
              </div>
              <div className="svc-signal-spark">
                <Sparkline data={sig.spark} color={sig.breach ? "var(--err)" : "var(--primary)"} />
              </div>
            </div>
          ))}
        </div>
      ) : readingsLoaded && readings.length === 0 ? (
        <div className="muted" style={{ fontSize: 13, padding: "8px 2px" }}>
          No metric health checks are set to show on this service. Mark a metric
          check “show on service page” from the Health checks tab, or switch back
          to Traces.
        </div>
      ) : (
        <div className="svc-signals">
          {readings.map((rd) => (
            <div key={rd.rule_id} className="svc-signal">
              <span className="svc-signal-k">{rd.name}</span>
              <div className="svc-signal-row">
                <span className={`svc-signal-v ${rd.breached ? "br" : ""}`}>
                  {rd.has_value ? formatNumber(rd.value ?? 0) : "—"}
                </span>
                {rd.unit && <span className="svc-signal-u">{rd.unit}</span>}
              </div>
              <div className="svc-signal-spark muted" style={{ fontSize: 11.5, display: "flex", alignItems: "center" }}>
                {OP_GLYPH[rd.operator] ?? rd.operator} {formatNumber(rd.threshold)}{rd.unit ? ` ${rd.unit}` : ""}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// ── health checks viewer ───────────────────────────────────────
function HealthChecksView({
  rules,
  firingByRule,
  failing,
  openErrorCount,
  onEdit,
  onSelectCheck,
  window: win,
  wide,
}: {
  rules: AlertRule[];
  firingByRule: Map<string, AlertInstance>;
  failing: number;
  // Unacknowledged error traces behind the built-in "error span" check.
  openErrorCount: number;
  onEdit: () => void;
  // Open the detail blade for a check: a configured rule, or the built-in
  // error-span pseudo-check (rule null, builtin true).
  onSelectCheck: (sel: { rule: AlertRule | null; builtin: boolean }) => void;
  window: string;
  wide?: boolean;
}) {
  void win;
  void openErrorCount;
  // Health is driven solely by configured health checks now — the old
  // built-in "error span detected" pseudo-check is gone, so a service with
  // error traces but no failing check reads healthy.
  return (
    <div className="svc-section" style={wide ? { maxWidth: 900 } : undefined}>
      <div className="svc-section-head">
        <span className="svc-section-title">🔔 Health checks</span>
        <span className="svc-section-sub">Service is unhealthy if <b>any</b> check is firing</span>
        <span style={{ flex: 1 }} />
        <button type="button" className="btn btn--link" onClick={onEdit}>✎ Edit checks</button>
      </div>
      <div className={`svc-health-summary ${failing === 0 ? "ok" : ""}`}>
        <div className="svc-health-summary-icon">{failing === 0 ? "✓" : "!"}</div>
        <div className="svc-health-summary-text">
          <div className="svc-health-summary-title">
            {failing === 0
              ? rules.length === 0
                ? "No health checks configured"
                : "All checks passing"
              : `${failing} check${failing === 1 ? "" : "s"} failing`}
          </div>
          <div className="svc-health-summary-sub">
            {failing === 0
              ? rules.length === 0
                ? "Add a check to define what makes this service unhealthy."
                : "All evaluations are within their thresholds."
              : "Service is unhealthy — the failing checks below explain why."}
          </div>
        </div>
      </div>
      <div className="svc-checks">
        {rules.map((r) => {
          const inst = firingByRule.get(r.id);
          const fail = !!inst;
          return (
            <div
              key={r.id}
              className={`svc-check ${fail ? "fail" : "ok"}`}
              role="button"
              tabIndex={0}
              style={{ cursor: "pointer" }}
              title="See this check's result"
              onClick={() => onSelectCheck({ rule: r, builtin: false })}
              onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onSelectCheck({ rule: r, builtin: false }); } }}
            >
              <span className="svc-check-pip" />
              <div className="svc-check-main">
                <div className="svc-check-name">
                  {r.name}
                  {alertSignalLabel(r.signal) && (
                    <span className="m-rule-badge" style={{ marginLeft: 6 }}>{alertSignalLabel(r.signal)}</span>
                  )}
                </div>
                {/* Condition reads per signal (metric / pushed / log /
                    failed-trace / response-time), not metric-only. */}
                <div className="svc-check-cond mono">{alertCondition(r)}</div>
              </div>
              <div className="svc-check-status">
                <span className="svc-check-badge">{fail ? "Firing" : "Passing"}</span>
                <span className="svc-check-since">
                  {inst ? `for ${formatRelative(inst.started_at).replace(" ago", "")}` : `severity ${r.severity}`}
                </span>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

// ── dependencies (from trace neighbors) ────────────────────────
//
// Rendered with the same flow-graph the integration and trace views
// use, so a service's neighborhood reads like everywhere else in the
// product: this service in the middle, callers flowing in from the
// left, callees out to the right, edge labels carrying trace volume.
function Dependencies({
  neighbors,
  name,
  ownTraceCount,
  ownStatus,
}: {
  neighbors: NeighborsResponse | null;
  name: string;
  ownTraceCount: number;
  ownStatus?: ServiceStatus;
}) {
  const up = neighbors?.upstream ?? [];
  const down = neighbors?.downstream ?? [];
  const { nodes, edges } = useMemo(() => {
    // A neighbor can appear on both sides (mutual calls) — one node,
    // both edges.
    const byName = new Map<string, FlowNode>();
    byName.set(name, {
      service_name: name,
      trace_count: ownTraceCount,
      error_trace_count: 0,
      status: ownStatus,
    });
    for (const n of [...up, ...down]) {
      const prev = byName.get(n.service_name);
      if (!prev) {
        byName.set(n.service_name, {
          service_name: n.service_name,
          trace_count: n.trace_count,
          error_trace_count: n.error_count,
        });
      } else if (prev.service_name !== name) {
        prev.trace_count = Math.max(prev.trace_count, n.trace_count);
        prev.error_trace_count = Math.max(prev.error_trace_count, n.error_count);
      }
    }
    const edges: FlowEdge[] = [
      ...up.map((u) => ({ source: u.service_name, target: name, call_count: u.trace_count, error_count: u.error_count })),
      ...down.map((d) => ({ source: name, target: d.service_name, call_count: d.trace_count, error_count: d.error_count })),
    ];
    return { nodes: [...byName.values()], edges };
  }, [up, down, name, ownTraceCount, ownStatus]);

  return (
    <div className="svc-section">
      <div className="svc-section-head">
        <span className="svc-section-title">Dependencies</span>
        <span className="svc-section-sub">Discovered from traces · selected window</span>
      </div>
      <div className="svc-deps">
        {up.length === 0 && down.length === 0 ? (
          <div className="placeholder" style={{ margin: 0 }}>No service-to-service calls in this window.</div>
        ) : (
          <ServiceFlowGraph nodes={nodes} edges={edges} highlight={name} />
        )}
      </div>
    </div>
  );
}

// ── metadata rail ──────────────────────────────────────────────
// IntegrationChip is a service's integration badge with an inline remove
// (×). Removing deletes the direct service.name=equals matcher; if the
// service is matched by a broader rule instead, the server reports
// removed=0 and we explain rather than silently doing nothing.
function IntegrationChip({
  integration,
  serviceName,
  onRemoved,
}: {
  integration: { id: string; name: string };
  serviceName: string;
  onRemoved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const remove = async () => {
    if (!window.confirm(`Remove ${serviceName} from "${integration.name}"?`)) return;
    setBusy(true);
    try {
      const r = await api.removeServiceFromIntegration(integration.id, serviceName);
      if (r.removed > 0) onRemoved();
      else
        window.alert(
          `${serviceName} is included in "${integration.name}" by a matching rule, not a direct link. Edit the integration's matchers to change it.`
        );
    } catch (e) {
      window.alert(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 2 }}>
      <Link className="chip" to={`/integrations/${integration.id}`}>{integration.name}</Link>
      <button
        type="button"
        title={`Remove ${serviceName} from ${integration.name}`}
        aria-label={`Remove from ${integration.name}`}
        onClick={remove}
        disabled={busy}
        style={{
          border: 0,
          background: "transparent",
          color: "var(--muted)",
          cursor: "pointer",
          fontSize: 13,
          lineHeight: 1,
          padding: "0 2px",
        }}
      >
        ×
      </button>
    </span>
  );
}

function MetadataPanel({ name, data, meta, onChanged }: { name: string; data: ServiceDetailResponse; meta: ServiceMetadata | null; onChanged: () => void }) {
  const initials = (s: string) => s.split(/\s+/).map((w) => w[0]).join("").slice(0, 2).toUpperCase();
  return (
    <div className="svc-section">
      <div className="svc-section-head"><span className="svc-section-title">Metadata</span></div>
      <div className="svc-meta-list">
        {meta?.owner && <div className="row"><span className="k">Owner</span><span className="v"><span className="svc-owner-av">{initials(meta.owner)}</span>{meta.owner}</span></div>}
        {meta?.on_call && <div className="row"><span className="k">On-call</span><span className="v"><span className="svc-owner-av">{initials(meta.on_call)}</span>{meta.on_call}</span></div>}
        {meta?.team && <div className="row"><span className="k">Team</span><span className="v mono">team:{meta.team}</span></div>}
        {meta?.repository && <div className="row"><span className="k">Repository</span><span className="v mono">{meta.repository}</span></div>}
        {meta?.runbook_url && <div className="row"><span className="k">Runbook</span><span className="v"><a href={meta.runbook_url} target="_blank" rel="noreferrer">{meta.runbook_url.replace(/^https?:\/\//, "")}</a></span></div>}
        <div className="row">
          <span className="k">Integrations</span>
          <span className="v" style={{ flexWrap: "wrap", gap: 4 }}>
            {data.integrations.length === 0 ? <span className="muted">none</span> : data.integrations.map((i) => (
              <IntegrationChip key={i.id} integration={i} serviceName={name} onRemoved={onChanged} />
            ))}
            <AddToIntegration serviceName={name} currentIntegrationIds={data.integrations.map((i) => i.id)} onAdded={onChanged} />
          </span>
        </div>
        <div className="row">
          <span className="k">Tags</span>
          <span className="v" style={{ flexWrap: "wrap", gap: 4 }}>
            {(data.tags ?? []).length === 0 ? <span className="muted">none</span> : (data.tags ?? []).map((t) => (
              <span key={t.id} className="chip">{t.name}</span>
            ))}
          </span>
        </div>
      </div>
    </div>
  );
}

// ── recent activity (from alert instances) ─────────────────────
function ActivityPanel({ instances, ruleIds }: { instances: AlertInstance[]; ruleIds: Set<string> }) {
  const rows = instances.filter((i) => ruleIds.has(i.alert_rule_id)).slice(0, 8);
  return (
    <div className="svc-section">
      <div className="svc-section-head"><span className="svc-section-title">Recent activity</span></div>
      {rows.length === 0 ? (
        <div style={{ padding: "12px 16px" }}><span className="muted" style={{ fontSize: 13 }}>No health-check events yet.</span></div>
      ) : (
        <div className="svc-feed">
          {rows.map((i) => (
            <div key={i.id} className="svc-feed-row">
              <div className={`svc-feed-icon ${i.state === "firing" ? "incident" : "resolved"}`}>{i.state === "firing" ? "!" : "✓"}</div>
              <div className="svc-feed-mid">
                <div className="svc-feed-title">{i.state === "firing" ? "Health check firing" : "Health check resolved"}: <b>{i.rule_name}</b></div>
                <div className="svc-feed-sub">{i.summary}</div>
              </div>
              <div className="svc-feed-time">{formatRelative(i.state === "firing" ? i.started_at : i.ended_at ?? i.started_at)}</div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

// ── notifications rail (channels the checks route to) ──────────
function NotificationsPanel({ rules, channels }: { rules: AlertRule[]; channels: NotificationChannel[] }) {
  const routed = useMemo(() => {
    const ids = new Set<string>();
    rules.forEach((r) => (r.channel_ids ?? []).forEach((c) => ids.add(c)));
    return channels.filter((c) => ids.has(c.id));
  }, [rules, channels]);
  return (
    <div className="svc-section">
      <div className="svc-section-head"><span className="svc-section-title">Notifications</span></div>
      <div style={{ padding: "12px 16px", display: "flex", flexDirection: "column", gap: 8 }}>
        {routed.length === 0 ? (
          <span className="muted" style={{ fontSize: 12.5 }}>No channels routed from this service's health checks.</span>
        ) : (
          routed.map((c) => (
            <div key={c.id} style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 12.5 }}>
              <span style={{ width: 26, height: 26, borderRadius: 6, background: "var(--surface-3)", border: "1px solid var(--border)", display: "inline-grid", placeItems: "center", fontFamily: "var(--mono)", fontWeight: 700, color: "var(--ink-2)" }}>{CHANNEL_GLYPH[c.kind] ?? "•"}</span>
              <div style={{ flex: 1 }}>
                <div style={{ fontWeight: 600, color: "var(--ink)" }}>{c.name}</div>
                <div style={{ fontSize: 11, color: "var(--muted)" }}>{c.kind}</div>
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  );
}
// ── EDIT MODE ──────────────────────────────────────────────────
function ServiceEdit({
  name,
  data,
  meta,
  allTags,
  tagsSaving,
  rules,
  channels,
  windowVal,
  onTagChange,
  onCreateTag,
  onChanged,
}: {
  name: string;
  data: ServiceDetailResponse;
  meta: ServiceMetadata | null;
  allTags: Tag[];
  tagsSaving: boolean;
  rules: AlertRule[];
  channels: NotificationChannel[];
  windowVal: string;
  onTagChange: (ids: string[]) => void;
  onCreateTag: (r: CreateTagRequest) => Promise<Tag>;
  onChanged: () => void;
}) {
  const [description, setDescription] = useState(meta?.description ?? "");
  const [team, setTeam] = useState(meta?.team ?? "");
  const [owner, setOwner] = useState(meta?.owner ?? "");
  const [onCall, setOnCall] = useState(meta?.on_call ?? "");
  const [repository, setRepository] = useState(meta?.repository ?? "");
  const [runbookURL, setRunbookURL] = useState(meta?.runbook_url ?? "");
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // "Mark as system" — saved immediately (it's catalog state, separate from the
  // metadata form's Save).
  const [isSystem, setIsSystem] = useState(!!data.is_system);
  const [systemKind, setSystemKind] = useState(data.system_kind ?? "");
  const [systemSaving, setSystemSaving] = useState(false);
  const saveSystem = async (nextIsSystem: boolean, nextKind: string) => {
    setIsSystem(nextIsSystem);
    setSystemKind(nextKind);
    setSystemSaving(true);
    try {
      await api.setServiceSystem(name, nextIsSystem, nextKind);
      onChanged();
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setSystemSaving(false);
    }
  };
  // Create a new system type from the picker (when the one you want isn't in the
  // catalog yet), add it to the catalog, and select it for this service.
  const addSystemType = async () => {
    const label = window.prompt("New system type name (e.g. NATS, ClickHouse):");
    if (!label || !label.trim()) return;
    const key = label.trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
    if (!key) {
      setErr("That name has no usable key — use letters or digits.");
      return;
    }
    setSystemSaving(true);
    setErr(null);
    try {
      await api.createSystemType({ key, label: label.trim(), is_system: true, detect_prefixes: [], checks: [] });
      const r = await api.listSystemTypes();
      setCatalogTypes(r.system_types ?? []);
      await saveSystem(true, key);
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setSystemSaving(false);
    }
  };

  // Apply a monitoring template — creates its health checks (skipping any
  // already present) and routes the chosen notification channels onto them so
  // they alert. applyKind is the kind being applied (null = picker closed);
  // applyVia chooses the endpoint: the flagged system kind ("system") or any
  // service-type kind ("general").
  const [applying, setApplying] = useState(false);
  const [applyMsg, setApplyMsg] = useState<string | null>(null);
  // Force the (self-fetching) HealthChecks list to re-load and scroll it into
  // view after a template is applied/removed, so the result is visible
  // immediately instead of only after a page reload.
  const [healthReloadKey, setHealthReloadKey] = useState(0);
  const healthChecksRef = useRef<HTMLDivElement>(null);
  const revealHealthChecks = (scroll: boolean) => {
    setHealthReloadKey((k) => k + 1);
    if (scroll) {
      // let the refetch + render settle, then bring the list into view
      setTimeout(() => healthChecksRef.current?.scrollIntoView({ behavior: "smooth", block: "start" }), 150);
    }
  };
  // applyKind holds a built-in kind OR a custom template id; applyVia routes:
  // "system" (flagged-system endpoint), "general" (built-in kind), "custom"
  // (a user-defined template by id). applyLabel is the panel header.
  const [applyKind, setApplyKind] = useState<string | null>(null);
  const [applyVia, setApplyVia] = useState<"system" | "general" | "custom">("general");
  const [applyLabel, setApplyLabel] = useState("");
  const [applyChannels, setApplyChannels] = useState<Set<string>>(new Set());
  const [manualKind, setManualKind] = useState("");
  // Auto-detected template kinds from the service's emitted metrics.
  const [suggestions, setSuggestions] = useState<{ kind: string; label: string; system: boolean; check_count: number; applied: boolean }[]>([]);
  const [removing, setRemoving] = useState(false);
  const [customTemplates, setCustomTemplates] = useState<MonitoringTemplate[]>([]);
  const [savingTemplate, setSavingTemplate] = useState(false);
  // Kinds already in use across the org — suggested in the kind combobox so
  // custom kinds you've used before reappear (the list "maintains itself").
  const [catalogTypes, setCatalogTypes] = useState<SystemType[]>([]);
  useEffect(() => {
    let alive = true;
    api
      .templateSuggestions(name)
      .then((r) => { if (alive) setSuggestions(r.suggestions ?? []); })
      .catch(() => {});
    api
      .listMonitoringTemplates()
      .then((r) => { if (alive) setCustomTemplates(r.templates ?? []); })
      .catch(() => {});
    api
      .listSystemTypes()
      .then((r) => { if (alive) setCatalogTypes(r.system_types ?? []); })
      .catch(() => {});
    return () => { alive = false; };
  }, [name]);
  const reloadCustomTemplates = () =>
    api.listMonitoringTemplates().then((r) => setCustomTemplates(r.templates ?? [])).catch(() => {});
  const toggleApplyChannel = (id: string) =>
    setApplyChannels((prev) => {
      const n = new Set(prev);
      n.has(id) ? n.delete(id) : n.add(id);
      return n;
    });
  const openApply = (kind: string, via: "system" | "general" | "custom", label: string) => {
    setApplyMsg(null);
    setApplyChannels(new Set());
    setApplyVia(via);
    setApplyKind(kind);
    setApplyLabel(label);
  };
  const reloadSuggestions = () =>
    api.templateSuggestions(name).then((r) => setSuggestions(r.suggestions ?? [])).catch(() => {});
  const applyTemplate = async () => {
    if (!applyKind) return;
    setApplying(true);
    setApplyMsg(null);
    try {
      const res =
        applyVia === "system"
          ? await api.applySystemTemplate(name, [...applyChannels])
          : applyVia === "custom"
            ? await api.applyCustomTemplate(name, applyKind, [...applyChannels])
            : await api.applyTemplate(name, applyKind, [...applyChannels]);
      if (res.message) {
        setApplyMsg(res.message);
      } else if (res.created === 0 && res.updated === 0 && res.skipped > 0) {
        setApplyMsg(`All ${res.skipped} check${res.skipped === 1 ? "" : "s"} already applied — nothing to add.`);
      } else {
        const parts = [`Created ${res.created} check${res.created === 1 ? "" : "s"}`];
        if (res.updated) parts.push(`re-routed ${res.updated}`);
        if (res.skipped) parts.push(`skipped ${res.skipped} already present`);
        setApplyMsg(parts.join(", ") + ".");
      }
      setApplyKind(null);
      onChanged();
      reloadSuggestions();
      revealHealthChecks(true);
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setApplying(false);
    }
  };
  // Remove a template's checks from this service (by built-in kind or custom id).
  const removeTemplate = async (opts: { kind?: string; templateId?: string }, label: string) => {
    if (typeof window !== "undefined" && !window.confirm(`Remove the ${label} checks from this service?`)) return;
    setRemoving(true);
    setApplyMsg(null);
    try {
      const res = await api.removeTemplate(name, opts);
      setApplyMsg(`Removed ${res.removed} check${res.removed === 1 ? "" : "s"}.`);
      onChanged();
      reloadSuggestions();
      revealHealthChecks(false);
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setRemoving(false);
    }
  };
  // Save this service's current metric/log checks as a reusable template.
  const saveAsTemplate = async () => {
    const tn = window.prompt("Name this template — captures this service's current metric + log health checks:", `${name} checks`);
    if (!tn || !tn.trim()) return;
    setSavingTemplate(true);
    setApplyMsg(null);
    try {
      await api.createMonitoringTemplate({ name: tn.trim(), from_service: name });
      setApplyMsg(`Saved template “${tn.trim()}”.`);
      reloadCustomTemplates();
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setSavingTemplate(false);
    }
  };

  // notification channels routed from this service's checks (union).
  const initialChannels = useMemo(() => {
    const ids = new Set<string>();
    rules.forEach((r) => (r.channel_ids ?? []).forEach((c) => ids.add(c)));
    return ids;
  }, [rules]);
  const [selectedChannels, setSelectedChannels] = useState<Set<string>>(initialChannels);
  useEffect(() => setSelectedChannels(initialChannels), [initialChannels]);

  const dirty =
    description !== (meta?.description ?? "") ||
    team !== (meta?.team ?? "") ||
    owner !== (meta?.owner ?? "") ||
    onCall !== (meta?.on_call ?? "") ||
    repository !== (meta?.repository ?? "") ||
    runbookURL !== (meta?.runbook_url ?? "");

  const save = async () => {
    setSaving(true);
    setErr(null);
    try {
      await api.updateServiceMetadata(name, { description, team, owner, on_call: onCall, repository, runbook_url: runbookURL });
      // Route the chosen channels onto every health check of this service.
      // Preserve each rule's signal + its matching spec — sending only the
      // metric spec would 400 a log/trace check ("spec.metric_name is
      // required") since those have an empty metric spec.
      const next = [...selectedChannels];
      await Promise.all(
        rules.map((r) =>
          api.updateAlertRule(r.id, {
            name: r.name,
            description: r.description,
            signal: r.signal as "metric" | "log" | "trace",
            severity: r.severity,
            enabled: r.enabled,
            channel_ids: next,
            spec: r.spec,
            log_spec: r.log_spec,
            trace_error_spec: r.trace_error_spec,
            trace_latency_spec: r.trace_latency_spec,
            trace_volume_spec: r.trace_volume_spec,
            service_name: r.service_name,
          }),
        ),
      );
      setSaved(true);
      onChanged();
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  const toggleChannel = (id: string) =>
    setSelectedChannels((prev) => {
      const n = new Set(prev);
      n.has(id) ? n.delete(id) : n.add(id);
      return n;
    });

  return (
    <div className="svc-edit-body">
      {/* Identity */}
      <div className="svc-form-card">
        <div className="svc-form-head">
          <div>
            <h2>Identity</h2>
            <div className="sub">Editable metadata. Name and namespace come from telemetry and aren't editable here.</div>
          </div>
          <span className="badge">Required</span>
        </div>
        <div className="svc-form-body">
          <div className="svc-form-grid">
            <div className="svc-field">
              <label className="svc-field-label">Service name <span className="hint">OTEL <span className="mono">service.name</span></span></label>
              <input className="svc-input mono" value={name} readOnly disabled />
            </div>
            <div className="svc-field">
              <label className="svc-field-label">Owner team</label>
              <input className="svc-input mono" value={team} onChange={(e) => setTeam(e.target.value)} placeholder="orders" />
            </div>
            <div className="svc-field col-2">
              <label className="svc-field-label">Description</label>
              <textarea className="svc-textarea" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="What this service does…" />
            </div>
            <div className="svc-field">
              <label className="svc-field-label">Owner</label>
              <input className="svc-input" value={owner} onChange={(e) => setOwner(e.target.value)} placeholder="Maya Chen" />
            </div>
            <div className="svc-field">
              <label className="svc-field-label">On-call</label>
              <input className="svc-input" value={onCall} onChange={(e) => setOnCall(e.target.value)} placeholder="Daniel Park" />
            </div>
            <div className="svc-field">
              <label className="svc-field-label">Repository</label>
              <input className="svc-input mono" value={repository} onChange={(e) => setRepository(e.target.value)} placeholder="org/repo" />
            </div>
            <div className="svc-field">
              <label className="svc-field-label">Runbook URL</label>
              <input className="svc-input mono" value={runbookURL} onChange={(e) => setRunbookURL(e.target.value)} placeholder="https://…" />
            </div>
            <div className="svc-field col-2">
              <label className="svc-field-label">Tags <span className="hint">searchable across Sluicio{tagsSaving ? " · saving…" : ""}</span></label>
              <TagPicker available={allTags} selectedIds={(data.tags ?? []).map((t) => t.id)} onChange={onTagChange} onCreate={onCreateTag} placeholder="add tag…" />
            </div>
            <div className="svc-field col-2">
              <label className="svc-field-label">
                System <span className="hint">treat this service as a monitored system (RabbitMQ, SQL Server, …){systemSaving ? " · saving…" : ""}</span>
              </label>
              <div style={{ display: "flex", alignItems: "center", gap: 12, flexWrap: "wrap" }}>
                <label style={{ display: "inline-flex", alignItems: "center", gap: 6, fontSize: 13 }}>
                  <input
                    type="checkbox"
                    checked={isSystem}
                    disabled={systemSaving}
                    onChange={(e) => saveSystem(e.target.checked, systemKind)}
                  />
                  Mark as a system
                </label>
                {isSystem && (
                  <>
                    {/* Pick the system type from the managed catalog (not free
                        text); "New type" adds one if it's not there yet. */}
                    <div style={{ minWidth: 220 }}>
                      <SearchableSelect
                        value={systemKind}
                        onChange={(k) => saveSystem(true, k)}
                        options={catalogTypes.map((t) => t.key)}
                        labelFor={(k) => catalogTypes.find((t) => t.key === k)?.label ?? k}
                        placeholder="Search system types…"
                        allLabel="— select a type —"
                      />
                    </div>
                    <button type="button" className="btn btn--sm" disabled={systemSaving} onClick={addSystemType}>
                      + New type
                    </button>
                  </>
                )}
                {isSystem && (
                  <span className="hint">It’ll appear in the Systems view; its health checks drive its status.</span>
                )}
              </div>
            </div>
            <div className="svc-field col-2">
              <label className="svc-field-label">
                Monitoring templates <span className="hint">starter health checks for this service's type — applied checks are editable afterward</span>
              </label>
              {applyKind ? (
                <div style={{ padding: 12, border: "1px solid var(--border)", borderRadius: 8, background: "var(--surface-2)" }}>
                  <div className="svc-field-label" style={{ marginBottom: 6 }}>
                    Apply {applyLabel} template — alert these checks to{" "}
                    <span className="hint">optional — pick channels so the checks notify, not just drive health</span>
                  </div>
                  {channels.length === 0 ? (
                    <span className="muted" style={{ fontSize: 13 }}>
                      No notification channels yet — add one on the <Link to="/alerts">Alerts</Link> page. You can apply now and route channels later.
                    </span>
                  ) : (
                    <div style={{ display: "flex", flexWrap: "wrap", gap: 12, margin: "4px 0 10px" }}>
                      {channels.map((c) => (
                        <label key={c.id} style={{ display: "inline-flex", alignItems: "center", gap: 6, fontSize: 13 }}>
                          <input
                            type="checkbox"
                            checked={applyChannels.has(c.id)}
                            onChange={() => toggleApplyChannel(c.id)}
                          />
                          {c.name} <span className="hint">{c.kind}</span>
                        </label>
                      ))}
                    </div>
                  )}
                  <div style={{ display: "flex", alignItems: "center", gap: 10, marginTop: 8, flexWrap: "wrap" }}>
                    <button type="button" className="btn" disabled={applying} onClick={applyTemplate}>
                      {applying
                        ? "Applying…"
                        : applyChannels.size > 0
                          ? `Create checks + alert ${applyChannels.size} channel${applyChannels.size === 1 ? "" : "s"}`
                          : "Create checks (health only)"}
                    </button>
                    <button type="button" className="btn" disabled={applying} onClick={() => setApplyKind(null)}>
                      Cancel
                    </button>
                  </div>
                </div>
              ) : (
                <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                  {isSystem && hasSystemTemplate(systemKind) && (
                    <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                      <button type="button" className="btn" disabled={systemSaving} onClick={() => openApply(systemKind, "system", systemKindLabel(systemKind))}>
                        Apply {systemKindLabel(systemKind)} template
                      </button>
                      <span className="hint">this flagged system's checks</span>
                    </div>
                  )}
                  {(() => {
                    const detected = suggestions.filter((s) => !(isSystem && systemKind && s.kind === systemKind));
                    if (detected.length === 0) return null;
                    return (
                      <div
                        style={{
                          padding: 12,
                          borderRadius: 8,
                          background: "var(--primary-soft)",
                          border: "1px solid color-mix(in oklab, var(--primary) 35%, transparent)",
                          borderLeft: "4px solid var(--primary)",
                          color: "var(--primary-ink)",
                          display: "flex",
                          flexDirection: "column",
                          gap: 8,
                        }}
                      >
                        <div style={{ fontWeight: 600, display: "flex", alignItems: "center", gap: 6 }}>
                          <span style={{ fontSize: 16 }}>⚙</span> System identification
                        </div>
                        {detected.map((s) => (
                          <div key={s.kind} style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                            <span style={{ flex: 1, minWidth: 160 }}>
                              Identified as <strong>{s.label}</strong> from emitted metrics — {s.check_count} starter check{s.check_count === 1 ? "" : "s"}
                              {s.applied ? " · applied ✓" : " ready to apply."}
                            </span>
                            {s.applied ? (
                              <button type="button" className="btn" disabled={removing} onClick={() => removeTemplate({ kind: s.kind }, s.label)}>
                                Remove
                              </button>
                            ) : (
                              <button type="button" className="btn primary" onClick={() => openApply(s.kind, "general", s.label)}>
                                Apply {s.label} template
                              </button>
                            )}
                          </div>
                        ))}
                      </div>
                    );
                  })()}
                  {customTemplates.map((t) => {
                    const applied = t.checks.length > 0 && t.checks.every((c) => rules.some((rl) => rl.name === c.name));
                    return (
                      <div key={t.id} style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                        {applied ? (
                          <button type="button" className="btn" disabled={removing} onClick={() => removeTemplate({ templateId: t.id }, t.name)}>
                            Remove
                          </button>
                        ) : (
                          <button type="button" className="btn" onClick={() => openApply(t.id, "custom", t.name)}>
                            Apply {t.name}
                          </button>
                        )}
                        <span className="hint">custom · {t.checks.length} check{t.checks.length === 1 ? "" : "s"}{applied ? " · applied ✓" : ""}</span>
                      </div>
                    );
                  })}
                  <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
                    <select className="svc-input" style={{ maxWidth: 220 }} value={manualKind} onChange={(e) => setManualKind(e.target.value)}>
                      <option value="">— or pick a type manually —</option>
                      {SERVICE_TEMPLATE_KINDS.map((k) => (
                        <option key={k.value} value={k.value}>{k.label}</option>
                      ))}
                    </select>
                    <button type="button" className="btn" disabled={!manualKind} onClick={() => manualKind && openApply(manualKind, "general", templateKindLabel(manualKind))}>
                      Apply
                    </button>
                  </div>
                  {rules.length > 0 && (
                    <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap", marginTop: 2 }}>
                      <button type="button" className="btn" disabled={savingTemplate} onClick={saveAsTemplate}>
                        {savingTemplate ? "Saving…" : "Save these checks as a template"}
                      </button>
                      <span className="hint">capture this service's checks for reuse · manage on the <Link to="/monitoring-templates">Templates</Link> page</span>
                    </div>
                  )}
                  {applyMsg && (
                    <span
                      style={{
                        display: "inline-flex",
                        alignItems: "center",
                        gap: 6,
                        fontSize: 12.5,
                        fontWeight: 600,
                        padding: "3px 8px",
                        borderRadius: 6,
                        background: "var(--ok-soft, var(--primary-soft))",
                        color: "var(--ok-ink, var(--primary-ink))",
                      }}
                    >
                      ✓ {applyMsg}
                    </span>
                  )}
                </div>
              )}
            </div>
          </div>
        </div>
      </div>

      {/* Current values — value tiles for "show on service page" health
          checks (the unified custom-metric display). This is the edit screen,
          separate from the overview's golden-signals toggle. */}
      <ServiceReadingTiles serviceName={name} />

      {/* Health checks — the HealthChecks component is itself a card with
          its own list + add/edit editor. Wrapped so applying a template can
          scroll it into view; reloadKey forces a re-fetch after apply/remove. */}
      <div ref={healthChecksRef} style={{ scrollMarginTop: 12 }}>
        <HealthChecks scope="service" target={name} window={windowVal} reloadKey={healthReloadKey} />
      </div>

      {/* Notifications */}
      <div className="svc-form-card">
        <div className="svc-form-head">
          <div>
            <h2>Notifications</h2>
            <div className="sub">Channels alerted when this service's checks fire. Applied to every check on Save.</div>
          </div>
        </div>
        <div className="svc-form-body">
          {channels.length === 0 ? (
            <span className="muted" style={{ fontSize: 13 }}>No channels yet — add one on the <Link to="/alerts">Alerts</Link> page.</span>
          ) : (
            <div className="m-intg-list">
              {channels.map((c) => {
                const on = selectedChannels.has(c.id);
                return (
                  <button key={c.id} type="button" className={`m-intg ${on ? "on" : ""}`} onClick={() => toggleChannel(c.id)}>
                    <span className="m-intg-icon">{CHANNEL_GLYPH[c.kind] ?? "•"}</span>
                    <div className="m-intg-mid"><div className="m-intg-name">{c.name}</div><div className="m-intg-kind">{c.kind}</div></div>
                    <span className="m-intg-tick">{on ? "✓" : ""}</span>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      </div>

      {/* Advanced (facet + attribute-mapping config) */}
      <div className="svc-form-card">
        <div className="svc-form-head"><div><h2>Advanced</h2><div className="sub">Service facets and attribute mappings.</div></div></div>
        <div style={{ padding: 16, display: "flex", flexDirection: "column", gap: 16 }}>
          <ServiceFacetsEditor serviceName={name} onChanged={onChanged} />
          <FacetMappingsEditor serviceName={name} onChanged={onChanged} />
        </div>
      </div>

      {/* Save bar */}
      <div className="svc-savebar">
        <div className="svc-savebar-msg">
          <span className="dot" style={{ background: dirty ? "var(--warn)" : "var(--ok)" }} />
          {err ? <span style={{ color: "var(--err-ink)" }}>{err}</span> : saved && !dirty ? <span>Saved.</span> : <span><b>{dirty ? "Unsaved changes" : "Identity & notifications"}</b> · health checks + tags save instantly</span>}
        </div>
        <button className="btn primary" disabled={saving} onClick={save}>{saving ? "Saving…" : "Save changes"}</button>
      </div>
    </div>
  );
}
