// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Dashboard / Home — the "how are we doing today?" page.
//
// v2 of this page is fully customisable: each user keeps one or more
// named Dashboards (saved server-side via /api/v1/dashboards) and the
// active dashboard chooses both which integrations appear and which
// widget every card renders (traffic sparkline, error count, or
// latency p95). The legacy "show every integration with a traffic
// sparkline" view is preserved as a seeded org-shared dashboard called
// "All integrations" so a fresh cell looks exactly like it did before.
//
// Auto-include semantics:
//   - autoIncludeAll=true  → render every integration in the org; items
//     act as per-integration widget-type overrides. Lets the user say
//     "show me everything but I want the Payments card to be latency".
//   - autoIncludeAll=false → render only the integrations listed in
//     items, in items[].position order. The curated case.
//
// Data: still drives off api.listIntegrations(window) — Integration[].
// Per-card sparkline / error / latency values use deterministic
// pseudo-data because the cell-api doesn't yet expose per-integration
// timeseries; swap in real series when the endpoint lands.

import { useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import { useAccess } from "../lib/useAccess";
import { useCurrentUser } from "../lib/useCurrentUser";
import {
  Donut,
  KpiCard,
  Sparkline,
  StatusPip,
  pipForStatus,
} from "../components/primitives";
import type { PipKind } from "../components/primitives";
import type {
  Dashboard,
  DashboardItem,
  DashboardItemRequest,
  DashboardWidgetType,
  Integration,
  ServiceSummary,
  System,
} from "../api/types";
import { DASHBOARD_WIDGET_LABELS, DASHBOARD_WIDGET_PICKER } from "../api/types";
import { systemKindLabel } from "../lib/systemKinds";
import SearchableSelect from "../components/SearchableSelect";
import OnboardingGuide from "../components/OnboardingGuide";
import { formatNumber } from "../lib/format";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";

// Remembers the last-active dashboard between sessions. Server stores
// is_default; this is purely a per-browser nicety so a power user with
// many dashboards lands back on the one they were just looking at.
const LAST_DASHBOARD_KEY = "conduit.health.lastDashboardId";

// Severity ordering for rolling a dashboard's integrations up to one tab
// pip: the highest-ranked status among them wins (red beats green beats
// neutral). warn sits above ok so a future "degraded" status surfaces.
const PIP_RANK: Record<PipKind, number> = { muted: 0, ok: 1, warn: 2, err: 3 };

// dashboardPipTitle is the tooltip on a tab's status pip — a plain-English
// read of the rolled-up state so the colour isn't the only signal.
function dashboardPipTitle(kind: PipKind): string {
  switch (kind) {
    case "err":
      return "One or more integrations unhealthy or degraded";
    case "warn":
      return "One or more integrations degraded";
    case "ok":
      return "All integrations healthy";
    default:
      return "No active integrations";
  }
}

export default function Health() {
  usePageTitle("Dashboard");
  const [windowVal] = useTimeWindow();

  // Environment label from the cell-wide system setting (Settings →
  // System settings, #27), so the dashboard subtitle reflects reality
  // instead of a hardcoded "production". Falls back until loaded.
  const [env, setEnv] = useState("production");
  useEffect(() => {
    let cancelled = false;
    api
      .getSystemSettings()
      .then((s) => {
        if (!cancelled && s.environment) setEnv(s.environment);
      })
      .catch(() => {
        /* keep the fallback */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Integrations: source of truth for which cards *could* appear.
  const [integrations, setIntegrations] = useState<Integration[] | null>(null);
  const [intError, setIntError] = useState<string | null>(null);
  const [intLoading, setIntLoading] = useState(true);

  // Dashboards: the user's saved layouts. Editing happens locally
  // (`draft`) and is committed via Save.
  const access = useAccess();
  const { can } = useCurrentUser();
  const canOrgWrite = can("integration.write");
  const [dashboards, setDashboards] = useState<Dashboard[] | null>(null);
  const [dashError, setDashError] = useState<string | null>(null);
  const [activeId, setActiveId] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<Dashboard | null>(null);
  const [saving, setSaving] = useState(false);
  const [newOpen, setNewOpen] = useState(false);

  // ── data loading ────────────────────────────────────────────────

  useEffect(() => {
    setIntLoading(true);
    setIntError(null);
    api
      .listIntegrations(windowVal, { series: true })
      .then((r) => setIntegrations(r.integrations ?? []))
      .catch((e) => setIntError(String(e.message ?? e)))
      .finally(() => setIntLoading(false));
  }, [windowVal]);

  // Dashboards load once — they don't depend on the time window. The
  // editor reloads from the server after save/delete so any new
  // computed timestamps stay in sync.
  const loadDashboards = useCallback(() => {
    setDashError(null);
    api
      .listDashboards()
      .then((r) => setDashboards(r.dashboards ?? []))
      .catch((e) => setDashError(String(e.message ?? e)));
  }, []);
  useEffect(() => {
    loadDashboards();
  }, [loadDashboards]);

  // Pick the active dashboard once both lists have loaded. Preference
  // order: localStorage last-used > server isDefault > first by position.
  useEffect(() => {
    if (dashboards === null) return;
    if (dashboards.length === 0) {
      setActiveId(null);
      return;
    }
    if (activeId && dashboards.some((d) => d.id === activeId)) return;
    const remembered =
      typeof window !== "undefined"
        ? window.localStorage.getItem(LAST_DASHBOARD_KEY)
        : null;
    const remembered_match = remembered
      ? dashboards.find((d) => d.id === remembered)
      : undefined;
    const next =
      remembered_match ??
      dashboards.find((d) => d.isDefault) ??
      dashboards[0];
    setActiveId(next.id);
  }, [dashboards, activeId]);

  // Persist the user's selection so refreshes don't jump around.
  useEffect(() => {
    if (activeId && typeof window !== "undefined") {
      window.localStorage.setItem(LAST_DASHBOARD_KEY, activeId);
    }
  }, [activeId]);

  // Keep the local edit draft in sync with the active dashboard, and
  // drop edit mode whenever the user switches tabs (avoid losing edits
  // silently — the Save button is the only commit).
  useEffect(() => {
    const active = dashboards?.find((d) => d.id === activeId) ?? null;
    setDraft(active ? structuredClone(active) : null);
    setEditing(false);
  }, [dashboards, activeId]);

  // ── derived: the cards to render ────────────────────────────────

  const active = useMemo(
    () => dashboards?.find((d) => d.id === activeId) ?? null,
    [dashboards, activeId],
  );

  // visibleSource is the dashboard whose items drive the layout —
  // the draft while editing, the saved copy otherwise. Keeping it as
  // one variable means the renderer below is identical for both modes.
  const visibleSource: Dashboard | null = editing ? draft : active;

  const cards = useMemo(
    () => composeCards(integrations ?? [], visibleSource),
    [integrations, visibleSource],
  );

  // Systems pinned to the dashboard render as their own strip (status +
  // error count + kind), driven by the flagged-systems list.
  const [systems, setSystems] = useState<ServiceSummary[]>([]);
  useEffect(() => {
    api
      .listServices(windowVal)
      .then((r) => setSystems(r.services ?? []))
      .catch(() => setSystems([]));
  }, [windowVal]);

  const systemCards = useMemo(() => {
    const byName = new Map(systems.map((s) => [s.service_name, s] as const));
    return (visibleSource?.items ?? [])
      .filter((i) => i.entityKind === "system")
      .slice()
      .sort((a, b) => a.position - b.position)
      .map((i) => ({ name: i.systemName ?? "", summary: byName.get(i.systemName ?? "") }));
  }, [visibleSource, systems]);

  const addSystem = useCallback((name: string) => {
    setDraft((d) => {
      if (!d) return d;
      if (d.items.some((i) => i.entityKind === "system" && i.systemName === name)) return d;
      return { ...d, items: [...d.items, synthesizeSystemItem(name, d.items.length)] };
    });
  }, []);
  const removeSystem = useCallback((name: string) => {
    setDraft((d) =>
      d ? { ...d, items: d.items.filter((i) => !(i.entityKind === "system" && i.systemName === name)) } : d,
    );
  }, []);

  const summary = useMemo(() => summarise(integrations ?? []), [integrations]);

  // System entities (phase 2/4) for the org-wide "systems running" KPI — their
  // own list with a rollup health status, independent of the pinned cards.
  const [systemEntities, setSystemEntities] = useState<System[]>([]);
  useEffect(() => {
    api.listSystems().then((r) => setSystemEntities(r.systems ?? [])).catch(() => setSystemEntities([]));
  }, []);
  const sysSummary = useMemo(() => summariseSystems(systemEntities), [systemEntities]);

  // Per-dashboard rolled-up health for the tab pips: the worst status
  // among the integrations that dashboard shows (composeCards honours its
  // scope — every integration for an auto-include dashboard, only the
  // curated set otherwise). So a tab turns red the moment one of its
  // integrations is unhealthy, green when all are healthy, neutral when
  // it's all quiet / empty.
  const statusByDashboard = useMemo(() => {
    const m = new Map<string, PipKind>();
    for (const d of dashboards ?? []) {
      let worst: PipKind = "muted";
      for (const c of composeCards(integrations ?? [], d)) {
        const k = pipForStatus(c.integration.status);
        if (PIP_RANK[k] > PIP_RANK[worst]) worst = k;
      }
      m.set(d.id, worst);
    }
    return m;
  }, [dashboards, integrations]);

  const needsAttention = useMemo(() => {
    const top = (integrations ?? [])
      .slice()
      .sort(
        (a, b) =>
          (b.error_trace_count ?? 0) - (a.error_trace_count ?? 0) ||
          (b.unhealthy_count ?? 0) - (a.unhealthy_count ?? 0),
      )[0];
    // Only call it "needs attention" if there's actually something to
    // pay attention to in the current window. Otherwise the empty-state
    // ("All clear" / "No incidents in the current window") takes over.
    if (!top) return undefined;
    if ((top.error_trace_count ?? 0) === 0 && (top.unhealthy_count ?? 0) === 0) {
      return undefined;
    }
    return top;
  }, [integrations]);

  // ── edit handlers ───────────────────────────────────────────────

  const updateDraft = useCallback((patch: Partial<Dashboard>) => {
    setDraft((d) => (d ? { ...d, ...patch } : d));
  }, []);

  const setCardWidget = useCallback(
    (integrationId: string, widget: DashboardWidgetType) => {
      setDraft((d) => {
        if (!d) return d;
        const items = [...d.items];
        const idx = items.findIndex((i) => i.integrationId === integrationId);
        if (idx >= 0) {
          items[idx] = { ...items[idx], widgetType: widget };
        } else {
          items.push(synthesizeItem(integrationId, widget, items.length));
        }
        return { ...d, items };
      });
    },
    [],
  );

  const removeCard = useCallback(
    (integrationId: string) => {
      setDraft((d) => {
        if (!d) return d;
        // Materialize first so an auto-include dashboard keeps every
        // currently-visible card minus the removed one when it flips to
        // manual mode. Without this, items[] only stores overrides and
        // every non-overridden card would silently disappear.
        const materialized = materializeItems(d, integrations ?? []);
        const remaining = materialized.filter(
          (i) => i.integrationId !== integrationId,
        );
        // Removing implies the user is curating, so always commit to
        // manual mode. The Edit-mode "show every integration" checkbox
        // lets them revert deliberately.
        return { ...d, autoIncludeAll: false, items: remaining };
      });
    },
    [integrations],
  );

  const addCard = useCallback(
    (integrationId: string) => {
      setDraft((d) => {
        if (!d) return d;
        if (d.items.some((i) => i.integrationId === integrationId)) return d;
        const items = [
          ...d.items,
          synthesizeItem(integrationId, d.defaultWidgetType, d.items.length),
        ];
        return { ...d, items };
      });
    },
    [],
  );

  // quickRemove drops an integration from the active dashboard *without*
  // entering edit mode and saves immediately. This is what the small ×
  // on each card calls. Mirrors the in-draft removeCard logic so both
  // paths produce identical state on disk; the only difference is that
  // this one persists in one shot.
  const quickRemove = useCallback(
    async (integrationId: string) => {
      const target = dashboards?.find((d) => d.id === activeId);
      if (!target) return;
      const ok =
        typeof window !== "undefined"
          ? window.confirm("Remove this integration from the dashboard?")
          : true;
      if (!ok) return;

      // Materialize the full visible list first so an auto-include
      // dashboard keeps every other card after the flip to manual.
      // Without this the saved items[] would only contain the previous
      // explicit overrides and every other card would disappear.
      const materialized = materializeItems(target, integrations ?? []);
      const remaining = materialized.filter(
        (i) => i.integrationId !== integrationId,
      );

      setSaving(true);
      setDashError(null);
      try {
        const saved = await api.updateDashboard(target.id, {
          name: target.name,
          isDefault: target.isDefault,
          autoIncludeAll: false,
          defaultWidgetType: target.defaultWidgetType,
          position: target.position,
          items: remaining.map<DashboardItemRequest>(toItemRequest),
        });
        setDashboards((all) =>
          (all ?? []).map((d) => (d.id === saved.id ? saved : d)),
        );
      } catch (e: unknown) {
        setDashError(String((e as Error)?.message ?? e));
      } finally {
        setSaving(false);
      }
    },
    [activeId, dashboards, integrations],
  );

  const saveDraft = useCallback(async () => {
    if (!draft) return;
    setSaving(true);
    setDashError(null);
    try {
      // Reify any synthesized items into the server's request shape.
      // Server returns the persisted dashboard; replace the local copy.
      const saved = await api.updateDashboard(draft.id, {
        name: draft.name,
        isDefault: draft.isDefault,
        autoIncludeAll: draft.autoIncludeAll,
        defaultWidgetType: draft.defaultWidgetType,
        position: draft.position,
        items: draft.items.map<DashboardItemRequest>(toItemRequest),
      });
      setDashboards((all) =>
        (all ?? []).map((d) => (d.id === saved.id ? saved : d)),
      );
      setEditing(false);
    } catch (e: unknown) {
      setDashError(String((e as Error)?.message ?? e));
    } finally {
      setSaving(false);
    }
  }, [draft]);

  const cancelEdit = useCallback(() => {
    setDraft(active ? structuredClone(active) : null);
    setEditing(false);
  }, [active]);

  const deleteActive = useCallback(async () => {
    if (!active) return;
    if (dashboards && dashboards.length <= 1) {
      // Refuse to delete the last dashboard — without one the page has
      // nothing to show. Encourage the user to rename instead.
      setDashError(
        "Can't delete your only dashboard. Rename it or create another first.",
      );
      return;
    }
    const ok =
      typeof window !== "undefined"
        ? window.confirm(`Delete the "${active.name}" dashboard?`)
        : true;
    if (!ok) return;
    setSaving(true);
    setDashError(null);
    try {
      await api.deleteDashboard(active.id);
      setDashboards((all) => (all ?? []).filter((d) => d.id !== active.id));
      setActiveId(null);
      setEditing(false);
    } catch (e: unknown) {
      setDashError(String((e as Error)?.message ?? e));
    } finally {
      setSaving(false);
    }
  }, [active, dashboards]);

  const createDashboard = useCallback(
    async (name: string, start_with_all: boolean, groupId: string | null) => {
      setSaving(true);
      setDashError(null);
      try {
        const created = await api.createDashboard({
          name,
          autoIncludeAll: start_with_all,
          defaultWidgetType: "traffic_sparkline",
          ...(groupId ? { groupId } : {}),
        });
        setDashboards((all) => [...(all ?? []), created]);
        setActiveId(created.id);
        setEditing(true);
        setNewOpen(false);
      } catch (e: unknown) {
        setDashError(String((e as Error)?.message ?? e));
      } finally {
        setSaving(false);
      }
    },
    [],
  );

  // ── render ──────────────────────────────────────────────────────

  const loading = intLoading && !integrations;

  return (
    <div>
      <div className="mb-6 flex items-end justify-between">
        <div>
          <h1 className="text-3xl font-semibold tracking-tight">
            How are we doing today?
          </h1>
          <p className="mt-1 text-sm text-muted">
            Last {windowVal} · {env} · auto-refresh 10s
          </p>
        </div>
      </div>

      {/* Adaptive getting-started guide — self-gates: only renders for an
          org with no telemetry, or telemetry but no integrations. */}
      <OnboardingGuide />

      {intError && (
        <div className="alert alert--error" role="alert">
          {intError}
        </div>
      )}
      {dashError && (
        <div className="alert alert--error" role="alert">
          {dashError}
        </div>
      )}
      {loading && <div className="placeholder">Loading integrations…</div>}

      {integrations && (
        <>
          {/* KPI row — org-wide, dashboard-independent. */}
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-6">
            <KpiCard
              // When integrations are unhealthy the tile flips to a red,
              // attention-bordered "N of M unhealthy" so a degraded org reads
              // at a glance instead of hiding behind a healthy-looking
              // "running" count. "Unhealthy" (not "down") because an
              // integration can be degraded/erroring while still serving
              // traffic. All-healthy keeps the calm "running / total".
              label={summary.err > 0 ? "integrations unhealthy" : "integrations running"}
              to={summary.err > 0 ? "/integrations?status=unhealthy" : "/integrations"}
              tone={summary.err > 0 ? "err" : "default"}
              emphasis={summary.err > 0 ? "attention" : "none"}
              value={
                summary.err > 0 ? (
                  <>
                    {summary.err}
                    <span className="ml-1 text-base font-normal text-muted">
                      of {summary.total} unhealthy
                    </span>
                  </>
                ) : (
                  <>
                    {summary.running}
                    <span className="ml-1 text-base font-normal text-muted">
                      / {summary.total}
                    </span>
                  </>
                )
              }
              sub={
                <div className="flex flex-wrap items-center gap-3">
                  <StatusPip kind="ok" label={`${summary.ok} ok`} />
                  {summary.quiet > 0 && (
                    <StatusPip kind="muted" label={`${summary.quiet} quiet`} />
                  )}
                  {summary.err > 0 && (
                    <StatusPip kind="err" label={`${summary.err} unhealthy`} />
                  )}
                </div>
              }
            />

            <KpiCard
              // Mirrors "integrations running": green running/total when all
              // healthy, red "N of M unhealthy" when a system's member checks
              // are firing. Driven by the system entities' rollup status.
              label={sysSummary.err > 0 ? "systems unhealthy" : "systems running"}
              to={sysSummary.err > 0 ? "/systems?status=unhealthy" : "/systems"}
              tone={sysSummary.err > 0 ? "err" : "default"}
              emphasis={sysSummary.err > 0 ? "attention" : "none"}
              value={
                sysSummary.err > 0 ? (
                  <>
                    {sysSummary.err}
                    <span className="ml-1 text-base font-normal text-muted">
                      of {sysSummary.total} unhealthy
                    </span>
                  </>
                ) : (
                  <>
                    {sysSummary.running}
                    <span className="ml-1 text-base font-normal text-muted">
                      / {sysSummary.total}
                    </span>
                  </>
                )
              }
              sub={
                <div className="flex flex-wrap items-center gap-3">
                  <StatusPip kind="ok" label={`${sysSummary.ok} ok`} />
                  {sysSummary.quiet > 0 && (
                    <StatusPip kind="muted" label={`${sysSummary.quiet} quiet`} />
                  )}
                  {sysSummary.err > 0 && (
                    <StatusPip kind="err" label={`${sysSummary.err} unhealthy`} />
                  )}
                </div>
              }
            />

            <KpiCard
              label={`messages / ${windowVal}`}
              value={formatNumber(summary.messages)}
              sub={
                <Sparkline
                  data={summary.messagesSeries}
                  seed={summary.messages || 7}
                  width={220}
                  height={40}
                  tone="default"
                  stretch
                />
              }
            />

            <KpiCard
              label="success rate"
              value={
                <div className="flex items-center gap-3">
                  <Donut size={64} pct={summary.successRate} sub="ok" />
                  <div className="text-xs leading-relaxed text-muted">
                    ok&nbsp;&nbsp;
                    <span className="font-medium text-foreground">
                      {formatNumber(Math.max(0, summary.messages - summary.errors - summary.delayed))}
                    </span>
                    <br />
                    err&nbsp;
                    <span className="font-medium" style={{ color: "var(--err)" }}>
                      {formatNumber(summary.errors)}
                    </span>
                    {summary.delayed > 0 && (
                      <>
                        <br />
                        delayed&nbsp;
                        <span className="font-medium" style={{ color: "var(--warn)" }}>
                          {formatNumber(summary.delayed)}
                        </span>
                      </>
                    )}
                  </div>
                </div>
              }
            />

            <KpiCard
              label={`delayed / ${windowVal}`}
              tone={summary.delayed > 0 ? "warn" : "default"}
              value={formatNumber(summary.delayed)}
              sub={
                summary.delayed > 0 ? (
                  <>
                    {summary.delayedIntegrations} integration
                    {summary.delayedIntegrations === 1 ? "" : "s"} · missed SLA
                  </>
                ) : (
                  <>all traces within SLA</>
                )
              }
            />

            <KpiCard
              label="needs attention"
              emphasis={needsAttention ? "attention" : "none"}
              value={
                needsAttention ? (
                  <div className="text-xl font-semibold leading-tight">
                    {needsAttention.name}
                  </div>
                ) : (
                  <span className="text-base text-muted">All clear</span>
                )
              }
              sub={
                needsAttention ? (
                  <>
                    <div style={{ color: "var(--ink)" }}>
                      {needsAttention.error_trace_count ?? 0} error traces ·{" "}
                      {needsAttention.unhealthy_count ?? 0} unhealthy services
                    </div>
                    <Link
                      to={`/integrations/${needsAttention.id}`}
                      className="mt-2 inline-block text-sm font-medium underline-offset-2 hover:underline"
                      style={{ color: "var(--primary)" }}
                    >
                      open →
                    </Link>
                  </>
                ) : (
                  <span className="text-muted">
                    No incidents in the current window.
                  </span>
                )
              }
            />
          </div>

          {/* Dashboard picker + edit controls */}
          <div className="mt-8 flex flex-wrap items-center justify-between gap-3">
            <DashboardTabs
              dashboards={dashboards ?? []}
              activeId={activeId}
              statusByDashboard={statusByDashboard}
              onSelect={setActiveId}
              onNew={access.writeAnywhere ? () => setNewOpen(true) : undefined}
            />
            <div className="flex items-center gap-2 text-xs text-muted">
              {!editing && active && (active.canManage ?? true) && (
                <button
                  type="button"
                  onClick={() => setEditing(true)}
                  className="rounded-md border px-2.5 py-1 hover:border-border-strong"
                  style={{ borderColor: "var(--border)" }}
                >
                  edit dashboard
                </button>
              )}
              {editing && (
                <>
                  <button
                    type="button"
                    onClick={saveDraft}
                    disabled={saving}
                    className="rounded-md px-2.5 py-1 font-medium text-white"
                    style={{ background: "var(--primary)" }}
                  >
                    {saving ? "saving…" : "save"}
                  </button>
                  <button
                    type="button"
                    onClick={cancelEdit}
                    disabled={saving}
                    className="rounded-md border px-2.5 py-1"
                    style={{ borderColor: "var(--border)" }}
                  >
                    cancel
                  </button>
                  <button
                    type="button"
                    onClick={deleteActive}
                    disabled={saving}
                    className="rounded-md border px-2.5 py-1"
                    style={{ borderColor: "var(--border)", color: "var(--err)" }}
                  >
                    delete
                  </button>
                </>
              )}
            </div>
          </div>

          {/* Editor: title, mode, default widget */}
          {editing && draft && (
            <DashboardEditorBar
              draft={draft}
              integrations={integrations}
              cards={cards}
              systems={systems}
              pinnedSystems={systemCards.map((s) => s.name)}
              onChange={updateDraft}
              onAddCard={addCard}
              onAddSystem={addSystem}
            />
          )}

          {newOpen && (
            <NewDashboardDialog
              onCancel={() => setNewOpen(false)}
              onCreate={createDashboard}
              saving={saving}
              editorGroups={access.editorGroups}
              orgWide={canOrgWrite}
            />
          )}

          {/* Cards */}
          {cards.length === 0 && systemCards.length === 0 ? (
            <div className="placeholder mt-3">
              {integrations.length === 0 ? (
                <>
                  No integrations yet.{" "}
                  <Link to="/integrations/new" style={{ color: "var(--primary)" }}>
                    Create one →
                  </Link>
                </>
              ) : editing && draft && !draft.autoIncludeAll ? (
                <>This dashboard is empty. Use the picker above to add integrations or systems.</>
              ) : (
                <>This dashboard has nothing pinned to it.</>
              )}
            </div>
          ) : (
            <>
              {cards.length > 0 && (
                <div className="mt-3 grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
                  {cards.map((c) => (
                    <IntegrationCard
                      key={c.integration.id}
                      it={c.integration}
                      widget={c.widget}
                      editing={editing}
                      onWidgetChange={(w) => setCardWidget(c.integration.id, w)}
                      onRemove={() => removeCard(c.integration.id)}
                      onQuickRemove={() => quickRemove(c.integration.id)}
                      busy={saving}
                    />
                  ))}
                </div>
              )}
              {systemCards.length > 0 && (
                <div className="mt-3">
                  <div className="muted" style={{ fontSize: 13, fontWeight: 600, margin: "8px 0 6px" }}>Systems</div>
                  <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
                    {systemCards.map((sc) => (
                      <SystemCard
                        key={sc.name}
                        name={sc.name}
                        summary={sc.summary}
                        editing={editing}
                        onRemove={() => removeSystem(sc.name)}
                        busy={saving}
                      />
                    ))}
                  </div>
                </div>
              )}
            </>
          )}
        </>
      )}
    </div>
  );
}

// ── Dashboard tabs ──────────────────────────────────────────────────

interface TabsProps {
  dashboards: Dashboard[];
  activeId: string | null;
  // Rolled-up health per dashboard id, for the per-tab status pip.
  statusByDashboard: Map<string, PipKind>;
  onSelect: (id: string) => void;
  onNew?: () => void;
}

function DashboardTabs({ dashboards, activeId, statusByDashboard, onSelect, onNew }: TabsProps) {
  return (
    <div className="flex flex-wrap items-center gap-1">
      {dashboards.map((d) => {
        const active = d.id === activeId;
        const pip = statusByDashboard.get(d.id) ?? "muted";
        return (
          <button
            type="button"
            key={d.id}
            onClick={() => onSelect(d.id)}
            className="inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors"
            style={{
              background: active ? "var(--surface-2)" : "transparent",
              color: active ? "var(--ink)" : "var(--muted)",
              borderBottom: active ? "2px solid var(--primary)" : "2px solid transparent",
            }}
          >
            <StatusPip kind={pip} className="!gap-0" />
            <span title={dashboardPipTitle(pip)}>{d.name}</span>
            {d.isDefault && (
              <span
                className="ml-1.5 text-[10px] uppercase"
                style={{ color: "var(--muted)" }}
              >
                default
              </span>
            )}
          </button>
        );
      })}
      {onNew && (
      <button
        type="button"
        onClick={onNew}
        className="ml-1 rounded-md border px-2 py-1 text-xs text-muted hover:border-border-strong"
        style={{ borderColor: "var(--border)" }}
        title="New dashboard"
      >
        + new
      </button>
      )}
    </div>
  );
}

// ── Editor bar ──────────────────────────────────────────────────────

interface EditorBarProps {
  draft: Dashboard;
  integrations: Integration[];
  cards: ComposedCard[];
  systems: ServiceSummary[];
  pinnedSystems: string[];
  onChange: (patch: Partial<Dashboard>) => void;
  onAddCard: (integrationId: string) => void;
  onAddSystem: (name: string) => void;
}

function DashboardEditorBar({
  draft,
  integrations,
  cards,
  systems,
  pinnedSystems,
  onChange,
  onAddCard,
  onAddSystem,
}: EditorBarProps) {
  // Integrations not already in the visible card list — candidates for
  // the "+ add integration" picker. In auto mode this is empty (every
  // integration is already visible).
  const visible = new Set(cards.map((c) => c.integration.id));
  const candidates = integrations.filter((i) => !visible.has(i.id));
  // Systems not already pinned — candidates for the "+ add system" picker.
  const pinned = new Set(pinnedSystems);
  const systemCandidates = systems.filter((s) => !pinned.has(s.service_name));

  return (
    <div
      className="mt-3 flex flex-wrap items-center gap-3 rounded-md border p-3"
      style={{ borderColor: "var(--border)", background: "var(--surface-2)" }}
    >
      <label className="flex items-center gap-2 text-sm">
        <span className="text-muted">name</span>
        <input
          type="text"
          value={draft.name}
          onChange={(e) => onChange({ name: e.target.value })}
          className="rounded-md border bg-transparent px-2 py-1 text-sm"
          style={{ borderColor: "var(--border)", color: "var(--ink)" }}
        />
      </label>

      <label className="flex items-center gap-2 text-sm text-muted">
        <input
          type="checkbox"
          checked={draft.autoIncludeAll}
          onChange={(e) => onChange({ autoIncludeAll: e.target.checked })}
        />
        show every integration
      </label>

      <label className="flex items-center gap-2 text-sm text-muted">
        <input
          type="checkbox"
          checked={draft.isDefault}
          onChange={(e) => onChange({ isDefault: e.target.checked })}
        />
        my default
      </label>

      <label className="flex items-center gap-2 text-sm text-muted">
        default widget
        <WidgetSelect
          value={draft.defaultWidgetType}
          onChange={(w) => onChange({ defaultWidgetType: w })}
        />
      </label>

      {candidates.length > 0 && (
        <label className="ml-auto flex items-center gap-2 text-sm text-muted">
          add
          {/* Searchable picker (not a plain <select>): an org can have
              hundreds of integrations, so a typeahead is essential. Picking
              one adds it and the field resets to the placeholder. */}
          <SearchableSelect
            value=""
            onChange={(id) => {
              if (id) onAddCard(id);
            }}
            options={candidates.map((i) => i.id)}
            labelFor={(id) => candidates.find((c) => c.id === id)?.name ?? id}
            allLabel="choose integration…"
            placeholder="Filter integrations…"
            align="right"
          />
        </label>
      )}

      {systemCandidates.length > 0 && (
        <label
          className={`flex items-center gap-2 text-sm text-muted${candidates.length > 0 ? "" : " ml-auto"}`}
        >
          add system
          <SearchableSelect
            value=""
            onChange={(name) => {
              if (name) onAddSystem(name);
            }}
            options={systemCandidates.map((s) => s.service_name)}
            labelFor={(name) => {
              const s = systemCandidates.find((c) => c.service_name === name);
              return s ? `${name} · ${systemKindLabel(s.system_kind)}` : name;
            }}
            allLabel="choose system…"
            placeholder="Filter systems…"
            align="right"
          />
        </label>
      )}
    </div>
  );
}

// ── New-dashboard dialog ────────────────────────────────────────────

interface NewDashboardDialogProps {
  saving: boolean;
  onCreate: (name: string, autoIncludeAll: boolean, groupId: string | null) => void;
  onCancel: () => void;
  editorGroups: { id: string; name: string }[];
  orgWide: boolean;
}

function NewDashboardDialog({ saving, onCreate, onCancel, editorGroups, orgWide }: NewDashboardDialogProps) {
  const [name, setName] = useState("");
  const [autoAll, setAutoAll] = useState(false);
  // Team scoping (RBAC v2 A'): org editors may create org-wide ("") or
  // team dashboards; group-editors MUST pick one of their teams.
  const [groupId, setGroupId] = useState<string>(orgWide ? "" : (editorGroups[0]?.id ?? ""));
  const valid = name.trim().length > 0 && (orgWide || groupId !== "");
  return (
    <div
      className="mt-3 rounded-md border p-3"
      style={{ borderColor: "var(--border)", background: "var(--surface-2)" }}
    >
      <div className="text-sm font-medium">New dashboard</div>
      <div className="mt-2 flex flex-wrap items-center gap-3">
        <input
          autoFocus
          type="text"
          placeholder="e.g. Payments view"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            // Enter saves, matching the create button's guard. !saving
            // avoids a double submit since the input isn't disabled mid-create.
            if (e.key === "Enter" && valid && !saving) onCreate(name.trim(), autoAll, groupId || null);
          }}
          className="rounded-md border bg-transparent px-2 py-1 text-sm"
          style={{ borderColor: "var(--border)", color: "var(--ink)" }}
        />
        <label className="flex items-center gap-2 text-sm text-muted">
          <input
            type="checkbox"
            checked={autoAll}
            onChange={(e) => setAutoAll(e.target.checked)}
          />
          start with every integration
        </label>
        {(editorGroups.length > 0) && (
          <label className="flex items-center gap-2 text-sm text-muted">
            Team
            <select
              className="rounded-md border bg-transparent px-2 py-1 text-sm"
              style={{ borderColor: "var(--border)", color: "var(--ink)" }}
              value={groupId}
              onChange={(e) => setGroupId(e.target.value)}
            >
              {orgWide && <option value="">Org-wide</option>}
              {editorGroups.map((g) => (
                <option key={g.id} value={g.id}>{g.name}</option>
              ))}
            </select>
          </label>
        )}
        <button
          type="button"
          onClick={() => valid && onCreate(name.trim(), autoAll, groupId || null)}
          disabled={!valid || saving}
          className="rounded-md px-2.5 py-1 text-sm font-medium text-white"
          style={{ background: "var(--primary)", opacity: valid ? 1 : 0.5 }}
        >
          {saving ? "creating…" : "create"}
        </button>
        <button
          type="button"
          onClick={onCancel}
          disabled={saving}
          className="rounded-md border px-2.5 py-1 text-sm"
          style={{ borderColor: "var(--border)" }}
        >
          cancel
        </button>
      </div>
    </div>
  );
}

// ── Cards ───────────────────────────────────────────────────────────

interface CardProps {
  it: Integration;
  widget: DashboardWidgetType;
  editing: boolean;
  busy: boolean;
  onWidgetChange: (w: DashboardWidgetType) => void;
  // onRemove drops the integration from the local draft inside edit
  // mode (it's bundled into the next Save).
  onRemove: () => void;
  // onQuickRemove drops the integration and persists in one shot,
  // without entering edit mode. Wired to the small × in the corner.
  onQuickRemove: () => void;
}

function IntegrationCard({
  it,
  widget,
  editing,
  busy,
  onWidgetChange,
  onRemove,
  onQuickRemove,
}: CardProps) {
  const status = pipForStatus(it.status);
  const traces = it.trace_count ?? 0;
  const errors = it.error_trace_count ?? 0;
  const delayed = it.delayed_trace_count ?? 0;
  // Delayed traces are a missed-SLA failure, so they count against the
  // ok %. errors and delayed are disjoint server-side.
  const okPct = traces > 0 ? (Math.max(0, traces - errors - delayed) / traces) * 100 : null;
  const isErrored = status === "err";

  // Per the Color System: errored/attention cards keep --surface-2
  // body with a 4px --err left border. Do *not* fill the whole card
  // red — the goal is calm-with-a-spike, not red-overload.
  //
  // In edit mode the card stops being a link (otherwise every click on
  // the widget picker / remove button would navigate away) and shows
  // its config strip instead.
  const body = (
    <>
      {/* Header: status pip sits to the left of the title, on the same
          baseline, so the top-right corner stays clear for the × quick-
          remove. pr-7 reserves space so long names don't slide under it. */}
      <div className="min-w-0 pr-7">
        <div className="flex items-center gap-2">
          <StatusPip kind={status} />
          <span className="truncate text-base font-semibold text-foreground">
            {it.name}
          </span>
        </div>
        <div className="mt-0.5 font-mono text-xs text-muted">{it.slug}</div>
      </div>

      <div className="mt-3">
        <CardWidget it={it} widget={widget} status={status} />
      </div>

      <div
        className={`mt-2 grid gap-2 text-sm ${delayed > 0 ? "grid-cols-4" : "grid-cols-3"}`}
      >
        <div>
          <div className="text-[11px] uppercase tracking-wide text-muted">msgs</div>
          <div className="font-medium tabular-nums">{formatNumber(traces)}</div>
        </div>
        <div>
          <div className="text-[11px] uppercase tracking-wide text-muted">ok %</div>
          <div
            className="font-medium tabular-nums"
            style={{
              color:
                okPct === null
                  ? "var(--muted)"
                  : okPct >= 99
                    ? "var(--ok)"
                    : okPct >= 95
                      ? "var(--warn)"
                      : "var(--err)",
            }}
          >
            {okPct === null ? "—" : `${okPct.toFixed(1)}%`}
          </div>
        </div>
        <div>
          <div className="text-[11px] uppercase tracking-wide text-muted">errors</div>
          <div
            className="font-medium tabular-nums"
            style={{ color: errors > 0 ? "var(--err)" : "var(--ink)" }}
          >
            {formatNumber(errors)}
          </div>
        </div>
        {delayed > 0 && (
          <div>
            <div className="text-[11px] uppercase tracking-wide text-muted">delayed</div>
            <div className="font-medium tabular-nums" style={{ color: "var(--warn)" }}>
              {formatNumber(delayed)}
            </div>
          </div>
        )}
      </div>

      {editing && (
        <div
          className="mt-3 flex items-center justify-between gap-2 border-t pt-3 text-xs text-muted"
          style={{ borderColor: "var(--border)" }}
        >
          <label className="flex items-center gap-2">
            widget
            <WidgetSelect value={widget} onChange={onWidgetChange} />
          </label>
          <button
            type="button"
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              onRemove();
            }}
            className="rounded-md border px-2 py-0.5"
            style={{ borderColor: "var(--border)", color: "var(--err)" }}
          >
            remove
          </button>
        </div>
      )}
    </>
  );

  const sharedStyle = {
    borderColor: "var(--border)",
    background: "var(--surface-2)",
    color: "var(--ink)",
    borderLeft: isErrored ? "4px solid var(--err)" : undefined,
  } as const;

  // The quick-remove × sits in a relative wrapper, outside the Link,
  // so its onClick can never be swallowed by the card-wide navigation.
  // Edit-mode only (decision 2026-07-04): a destructive control on every
  // card in view mode invites accidental clicks; customisation lives
  // behind "edit dashboard".
  const removeButton = (
    <button
      type="button"
      aria-label="Remove from dashboard"
      title="Remove from dashboard"
      onClick={(e) => {
        e.preventDefault();
        e.stopPropagation();
        onQuickRemove();
      }}
      disabled={busy}
      className="absolute right-2 top-2 z-10 inline-flex h-6 w-6 items-center justify-center rounded-full text-sm leading-none opacity-40 transition-opacity hover:opacity-100"
      style={{
        background: "var(--surface)",
        border: "1px solid var(--border)",
        color: "var(--muted)",
      }}
    >
      ×
    </button>
  );

  if (editing) {
    return (
      <div className="relative">
        {removeButton}
        <div
          className="block rounded-xl border p-4 shadow-sm"
          style={sharedStyle}
        >
          {body}
        </div>
      </div>
    );
  }

  return (
    <div className="relative">
      <Link
        to={`/integrations/${it.id}`}
        className="group block rounded-xl border p-4 shadow-sm transition-colors hover:border-border-strong"
        style={sharedStyle}
      >
        {body}
      </Link>
    </div>
  );
}

// systemPip maps a service status to a card pip. unhealthy (a firing health
// check) is the loudest; errors are warn; no telemetry is muted.
function systemPip(status: string | undefined): PipKind {
  switch (status) {
    case "unhealthy":
      return "err";
    case "errors":
      return "warn";
    case "quiet":
      return "muted";
    case "ok":
      return "ok";
    default:
      return "muted";
  }
}

// SystemCard is the dashboard widget for a pinned system: status pip + name +
// kind badge + error count, linking to the service. summary is undefined when
// the system isn't in the current window's systems list (shown as unknown).
function SystemCard({
  name,
  summary,
  editing,
  busy,
  onRemove,
}: {
  name: string;
  summary?: ServiceSummary;
  editing: boolean;
  busy: boolean;
  onRemove: () => void;
}) {
  const pip = systemPip(summary?.status);
  const errors = summary?.error_trace_count ?? 0;
  const isErrored = pip === "err";
  const body = (
    <>
      <div className="min-w-0 pr-7">
        <div className="flex items-center gap-2">
          <StatusPip kind={pip} />
          <span className="truncate text-base font-semibold text-foreground">{name}</span>
        </div>
        <div className="mt-0.5 text-xs">
          {summary ? (
            <span className="badge-brand">⚙ {systemKindLabel(summary.system_kind)}</span>
          ) : (
            <span className="text-muted">no telemetry in window</span>
          )}
        </div>
      </div>
      <div className="mt-3 grid grid-cols-2 gap-2 text-sm">
        <div>
          <div className="text-[11px] uppercase tracking-wide text-muted">status</div>
          <div className="font-medium">{summary?.status ?? "unknown"}</div>
        </div>
        <div>
          <div className="text-[11px] uppercase tracking-wide text-muted">errors</div>
          <div className="font-medium tabular-nums" style={{ color: errors > 0 ? "var(--err)" : "var(--ink)" }}>
            {formatNumber(errors)}
          </div>
        </div>
      </div>
      {editing && (
        <div
          className="mt-3 flex items-center justify-end gap-2 border-t pt-3 text-xs text-muted"
          style={{ borderColor: "var(--border)" }}
        >
          <button
            type="button"
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              onRemove();
            }}
            className="rounded-md border px-2 py-0.5"
            style={{ borderColor: "var(--border)", color: "var(--err)" }}
          >
            remove
          </button>
        </div>
      )}
    </>
  );
  const sharedStyle = {
    borderColor: "var(--border)",
    background: "var(--surface-2)",
    color: "var(--ink)",
    borderLeft: isErrored ? "4px solid var(--err)" : undefined,
  } as const;

  const removeButton = (
    <button
      type="button"
      aria-label="Remove from dashboard"
      title="Remove from dashboard"
      onClick={(e) => {
        e.preventDefault();
        e.stopPropagation();
        onRemove();
      }}
      disabled={busy}
      className="absolute right-2 top-2 z-10 inline-flex h-6 w-6 items-center justify-center rounded-full text-sm leading-none opacity-40 transition-opacity hover:opacity-100"
      style={{ background: "var(--surface)", border: "1px solid var(--border)", color: "var(--muted)" }}
    >
      ×
    </button>
  );

  return (
    <div className="relative">
      {removeButton}
      <Link
        to={`/services/${encodeURIComponent(name)}`}
        className="group block rounded-xl border p-4 shadow-sm transition-colors hover:border-border-strong"
        style={sharedStyle}
      >
        {body}
      </Link>
    </div>
  );
}

// ── Widget renderer ────────────────────────────────────────────────

interface CardWidgetProps {
  it: Integration;
  widget: DashboardWidgetType;
  status: PipKind;
}

function CardWidget({ it, widget, status }: CardWidgetProps) {
  const tone = status === "err" ? "err" : status === "warn" ? "warn" : "default";
  switch (widget) {
    case "error_count":
      return <ErrorWidget it={it} />;
    case "latency_p95":
      return <LatencyWidget it={it} />;
    case "traffic_sparkline":
    default:
      return (
        // Real per-integration traffic (distinct traces bucketed across the
        // window). All-zeros for a quiet integration → a flat line. The seed
        // is only a fallback for older API responses without the series.
        <Sparkline
          data={it.traffic_series}
          seed={Math.abs(hashString(it.id)) || 1}
          width={320}
          height={44}
          tone={tone}
          stretch
        />
      );
  }
}

function ErrorWidget({ it }: { it: Integration }) {
  const errors = it.error_trace_count ?? 0;
  const traces = it.trace_count ?? 0;
  const rate = traces > 0 ? (errors / traces) * 100 : 0;
  const tone =
    errors === 0 ? "var(--ok)" : rate >= 5 ? "var(--err)" : "var(--warn)";
  return (
    <div className="flex items-baseline gap-3">
      <div
        className="text-3xl font-semibold tabular-nums leading-none"
        style={{ color: tone }}
      >
        {formatNumber(errors)}
      </div>
      <div className="text-xs text-muted">
        error traces · {traces > 0 ? `${rate.toFixed(2)}%` : "no traffic"}
      </div>
    </div>
  );
}

function LatencyWidget({ it }: { it: Integration }) {
  // The cell-api doesn't yet expose per-integration p95 in the list
  // response, so we surface a deterministic placeholder seeded from the
  // integration id (same pattern the v1 sparkline used). When the API
  // adds it.p95_duration_ms, replace this block with the real value.
  const p95 = Math.max(20, Math.round((Math.abs(hashString(it.id)) % 480) + 20));
  const tone =
    p95 < 200 ? "var(--ok)" : p95 < 800 ? "var(--warn)" : "var(--err)";
  return (
    <div className="flex items-baseline gap-3">
      <div
        className="text-3xl font-semibold tabular-nums leading-none"
        style={{ color: tone }}
      >
        {p95}
        <span className="ml-1 text-base font-normal text-muted">ms</span>
      </div>
      <div className="text-xs text-muted">p95 latency</div>
    </div>
  );
}

// ── Reusable widget picker ─────────────────────────────────────────

interface WidgetSelectProps {
  value: DashboardWidgetType;
  onChange: (w: DashboardWidgetType) => void;
}

function WidgetSelect({ value, onChange }: WidgetSelectProps) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value as DashboardWidgetType)}
      className="rounded-md border bg-transparent px-2 py-1 text-xs"
      style={{ borderColor: "var(--border)", color: "var(--ink)" }}
    >
      {DASHBOARD_WIDGET_PICKER.map((k) => (
        <option key={k} value={k}>
          {DASHBOARD_WIDGET_LABELS[k]}
        </option>
      ))}
    </select>
  );
}

// ── Composition: integrations × dashboard → cards ───────────────────

interface ComposedCard {
  integration: Integration;
  widget: DashboardWidgetType;
}

// composeCards turns the org's integration list and the active
// dashboard into the ordered list of cards to render.
//
//   autoIncludeAll  → every integration in the org, with items[] as
//                     widget-type overrides; ordering keeps items[]
//                     first (in declared position) then the rest by
//                     traffic, matching the legacy default.
//   !autoIncludeAll → only integrations explicitly listed in items[],
//                     in items[].position order.
//
// When no dashboard exists yet (the initial-empty state), fall back to
// the legacy "everything by traffic" view so the page isn't blank.
function composeCards(
  integrations: Integration[],
  dashboard: Dashboard | null,
): ComposedCard[] {
  const byId = new Map(integrations.map((i) => [i.id, i] as const));

  if (!dashboard) {
    return integrations
      .slice()
      .sort((a, b) => (b.trace_count ?? 0) - (a.trace_count ?? 0))
      .map((i) => ({ integration: i, widget: "traffic_sparkline" }));
  }

  const itemByIntegration = new Map(
    dashboard.items.map((i) => [i.integrationId, i] as const),
  );

  if (dashboard.autoIncludeAll) {
    // Items first (in declared order), then the rest by traffic.
    const inItems = dashboard.items
      .slice()
      .sort((a, b) => a.position - b.position)
      .map((i) => byId.get(i.integrationId))
      .filter((i): i is Integration => Boolean(i))
      .map((i) => ({
        integration: i,
        widget:
          itemByIntegration.get(i.id)?.widgetType ?? dashboard.defaultWidgetType,
      }));
    const itemIds = new Set(dashboard.items.map((i) => i.integrationId));
    const rest = integrations
      .filter((i) => !itemIds.has(i.id))
      .sort((a, b) => (b.trace_count ?? 0) - (a.trace_count ?? 0))
      .map((i) => ({
        integration: i,
        widget: dashboard.defaultWidgetType,
      }));
    return [...inItems, ...rest];
  }

  // Manual mode: only items[].
  return dashboard.items
    .slice()
    .sort((a, b) => a.position - b.position)
    .map((i) => {
      const it = byId.get(i.integrationId);
      if (!it) return null;
      return { integration: it, widget: i.widgetType };
    })
    .filter((c): c is ComposedCard => Boolean(c));
}

// synthesizeItem builds a draft-only DashboardItem for in-memory edits.
// The server assigns the real id and createdAt on save.
function synthesizeItem(
  integrationId: string,
  widgetType: DashboardWidgetType,
  position: number,
): DashboardItem {
  return {
    id: `draft-${integrationId}`,
    entityKind: "integration",
    integrationId,
    widgetType,
    position,
    createdAt: new Date().toISOString(),
  };
}

// synthesizeSystemItem builds a draft-only system DashboardItem.
function synthesizeSystemItem(systemName: string, position: number): DashboardItem {
  return {
    id: `draft-sys-${systemName}`,
    entityKind: "system",
    integrationId: "",
    systemName,
    widgetType: "system_health",
    position,
    createdAt: new Date().toISOString(),
  };
}

// materializeItems turns a dashboard into an explicit list of items
// representing every card the renderer would currently show. Used by
// the remove flows: in auto-include-all mode, items[] holds only the
// per-card widget *overrides* — filtering items[] then flipping to
// manual mode would drop every non-overridden card. Materializing
// first guarantees that "remove one" is what the user sees.
function materializeItems(
  dashboard: Dashboard,
  integrations: Integration[],
): DashboardItem[] {
  if (!dashboard.autoIncludeAll) {
    // Manual mode: items[] is already the source of truth.
    return [...dashboard.items];
  }
  const overrideById = new Map(
    dashboard.items.map((i) => [i.integrationId, i] as const),
  );
  // composeCards puts overrides first (in declared position) then
  // every remaining integration by traffic. Mirror that ordering here
  // so the persisted manual list keeps the same on-screen order.
  const overrides = dashboard.items
    .slice()
    .sort((a, b) => a.position - b.position)
    .map((i) => i.integrationId);
  const overrideSet = new Set(overrides);
  const rest = integrations
    .filter((i) => !overrideSet.has(i.id))
    .sort((a, b) => (b.trace_count ?? 0) - (a.trace_count ?? 0))
    .map((i) => i.id);
  const byId = new Map(integrations.map((i) => [i.id, i] as const));
  const ordered = [...overrides, ...rest].filter((id) => byId.has(id));
  const integrationItems: DashboardItem[] = ordered.map((id, idx) => {
    const existing = overrideById.get(id);
    return {
      id: existing?.id ?? `draft-${id}`,
      entityKind: "integration",
      integrationId: id,
      widgetType: existing?.widgetType ?? dashboard.defaultWidgetType,
      position: existing?.position ?? idx,
      createdAt: existing?.createdAt ?? new Date().toISOString(),
    };
  });
  // System items live in items[] too but aren't part of the auto-include
  // expansion — carry them through untouched.
  const systemItems = dashboard.items.filter((i) => i.entityKind === "system");
  return [...integrationItems, ...systemItems];
}

// toItemRequest maps a (possibly draft) DashboardItem to the write shape,
// emitting the integration or system form by entityKind.
function toItemRequest(i: DashboardItem, idx: number): DashboardItemRequest {
  if (i.entityKind === "system") {
    return {
      entityKind: "system",
      systemName: i.systemName,
      widgetType: "system_health",
      position: i.position ?? idx,
    };
  }
  return {
    entityKind: "integration",
    integrationId: i.integrationId,
    widgetType: i.widgetType,
    position: i.position ?? idx,
  };
}

// ── KPI summary (unchanged from v1) ────────────────────────────────

interface DashSummary {
  total: number;
  running: number;
  ok: number;
  // quiet = no traffic in the selected window. That's a fine/idle state,
  // not a warning — counted separately and shown as a neutral pip.
  quiet: number;
  err: number;
  messages: number;
  errors: number;
  delayed: number;
  delayedIntegrations: number;
  successRate: number;
  // Org-wide messages over the window, bucketed — the element-wise sum
  // of every integration's traffic_series (same real series the
  // per-integration cards draw), so the "messages / {window}" sparkline
  // reflects actual traffic instead of a decorative seeded shape.
  messagesSeries: number[];
}

// summariseSystems rolls system entities up to the "systems running" KPI,
// mirroring summarise() for integrations. running = total - unhealthy.
function summariseSystems(systems: System[]): { total: number; running: number; ok: number; quiet: number; err: number } {
  let ok = 0;
  let quiet = 0;
  let err = 0;
  for (const s of systems) {
    if (s.status === "ok") ok += 1;
    else if (s.status === "errors" || s.status === "unhealthy") err += 1;
    else if (s.status === "quiet") quiet += 1;
  }
  const total = systems.length;
  return { total, running: total - err, ok, quiet, err };
}

function summarise(ints: Integration[]): DashSummary {
  let ok = 0;
  let quiet = 0;
  let err = 0;
  let messages = 0;
  let errors = 0;
  let delayed = 0;
  let delayedIntegrations = 0;
  // Org-wide traffic series = element-wise sum of each integration's
  // bucketed traffic_series (all share the window's bucketing, so they
  // align by index). Defensive against differing lengths / absent series.
  const messagesSeries: number[] = [];
  for (const it of ints) {
    const s = it.status;
    if (s === "ok") ok += 1;
    else if (s === "errors") err += 1;
    else if (s === "unhealthy") err += 1;
    // "quiet" = no data in the window. It's a healthy idle state (it
    // still counts as running), not a warning — keep it in its own
    // bucket, matching the neutral per-card pip (pipForStatus).
    else if (s === "quiet") quiet += 1;
    messages += it.trace_count ?? 0;
    errors += it.error_trace_count ?? 0;
    const d = it.delayed_trace_count ?? 0;
    delayed += d;
    if (d > 0) delayedIntegrations += 1;
    const series = it.traffic_series;
    if (series) {
      for (let i = 0; i < series.length; i++) {
        messagesSeries[i] = (messagesSeries[i] ?? 0) + (series[i] ?? 0);
      }
    }
  }
  const total = ints.length;
  const running = total - err;
  // A delayed trace is a missed-SLA failure; errors and delayed are
  // disjoint (server-side), so both subtract from the success rate.
  const failures = errors + delayed;
  const successRate = messages > 0 ? Math.max(0, messages - failures) / messages : 1;
  return { total, running, ok, quiet, err, messages, errors, delayed, delayedIntegrations, successRate, messagesSeries };
}

function hashString(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) {
    h = (h << 5) - h + s.charCodeAt(i);
    h |= 0;
  }
  return h;
}
