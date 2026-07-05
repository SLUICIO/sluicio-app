// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Integration detail — Graph + Inspector (variant B from the Conduit
// handoff). Left column: the integration's flow graph (React Flow)
// with per-node error badges + a traffic chart underneath + the
// error-attribution breakdown. Right rail: ServiceInspector for the
// currently-selected service.
//
// Configuration (matchers, delete, etc.) lives in a collapsible
// "configuration" disclosure below the primary content so the page
// opens on the operational view the handoff calls for, rather than
// the admin form.

import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api } from "../api/client";
import ErrorBreakdown from "../components/ErrorBreakdown";
import IntegrationFlow from "../components/IntegrationFlow";
import IntegrationPageHeader from "../components/IntegrationPageHeader";
import IntegrationServicesGuide from "../components/IntegrationServicesGuide";
import IntegrationTabs from "../components/IntegrationTabs";
import { integrationProblemCount } from "../lib/integrationHealth";
import ServiceInspector from "../components/ServiceInspector";
import TagChip from "../components/tags/TagChip";
import TagPicker from "../components/tags/TagPicker";
import type {
  CreateTagRequest,
  FlowResponse,
  ServiceStatus,
  IntegrationDetail,
  ServiceDetailResponse,
  ServiceWidgetsResponse,
  Tag,
  WidgetResult,
} from "../api/types";
import { formatNumber } from "../lib/format";
import { useBreadcrumbLeaf } from "../lib/breadcrumb";
import { useCurrentUser } from "../lib/useCurrentUser";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";

export default function IntegrationDetailPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const [windowVal] = useTimeWindow();
  const { can } = useCurrentUser();
  const [data, setData] = useState<IntegrationDetail | null>(null);
  // Scoped manage (RBAC v2): the server says whether THIS integration is
  // manageable by the caller (org editors always; group-editors only when
  // every member service is in their managed scope).
  const canWrite = data?.can_manage ?? can("integration.write");
  const canDelete = canWrite && (can("integration.delete") || (data?.can_manage ?? false));
  const [flow, setFlow] = useState<FlowResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);

  // Currently-delayed message count for the metric strip. Polled
  // every 30s so the dashboard tile reflects what's actually open
  // right now, not a stale snapshot from page load. Distinct from
  // `error_message_count` — a delayed trace hasn't failed, it just
  // hasn't delivered within its SLA.
  const [delayedCount, setDelayedCount] = useState<number | null>(null);

  // Tags — the org vocabulary plus this integration's current tag
  // ids. We keep them separate so the picker can show every option
  // while we drive attachments through the API.
  const [allTags, setAllTags] = useState<Tag[]>([]);
  const [tagsLoading, setTagsLoading] = useState(false);

  // Inspector state — the currently-selected service in the flow.
  const [selectedService, setSelectedService] = useState<string | null>(null);
  const [serviceDetail, setServiceDetail] = useState<ServiceDetailResponse | null>(null);
  const [serviceWidgets, setServiceWidgets] = useState<WidgetResult[]>([]);
  const [serviceLoading, setServiceLoading] = useState(false);
  const [serviceError, setServiceError] = useState<string | null>(null);

  const refresh = () => {
    setLoading(true);
    setError(null);
    api
      .getIntegration(id, windowVal)
      .then(setData)
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
    api
      .integrationFlow(id, windowVal)
      .then(setFlow)
      .catch(() => setFlow(null));
    api
      .listTags()
      .then((d) => setAllTags(d.tags ?? []))
      .catch(() => setAllTags([]));
  };

  // applyTagSelection diffs the picker's selectedIds against the
  // currently-attached tags and fires attach/detach calls only for
  // what changed. Refresh after so the new chips reflect server state.
  const applyTagSelection = async (nextIds: string[]) => {
    if (!data) return;
    const currentIds = (data.tags ?? []).map((t) => t.id);
    const toAdd = nextIds.filter((x) => !currentIds.includes(x));
    const toRemove = currentIds.filter((x) => !nextIds.includes(x));
    setTagsLoading(true);
    try {
      await Promise.all([
        ...toAdd.map((tid) => api.attachIntegrationTag(id, tid)),
        ...toRemove.map((tid) => api.detachIntegrationTag(id, tid)),
      ]);
      // Optimistic update so the chips switch immediately; refresh
      // confirms server state.
      const next = allTags.filter((t) => nextIds.includes(t.id));
      setData({ ...data, tags: next });
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setTagsLoading(false);
      refresh();
    }
  };

  const createTag = async (req: CreateTagRequest): Promise<Tag> => {
    const created = await api.createTag(req);
    setAllTags((curr) => [...curr, created].sort((a, b) => a.name.localeCompare(b.name)));
    return created;
  };

  useEffect(refresh, [id, windowVal]);

  // Delayed-count tile: counts currently-firing trace-completion
  // alert instances on this integration. 30s cadence matches the
  // evaluator tick so the number can change by at most one cycle.
  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    const load = () =>
      api
        .listCompletionFirings(id)
        .then((r) => {
          if (cancelled) return;
          const open = r.firings.filter(
            (f) => f.state === "firing" && !f.handled_at,
          ).length;
          setDelayedCount(open);
        })
        .catch(() => {
          if (!cancelled) setDelayedCount(null);
        });
    load();
    const t = window.setInterval(load, 30000);
    return () => {
      cancelled = true;
      window.clearInterval(t);
    };
  }, [id]);

  // Preselect the highest-error service so the inspector opens with
  // something useful on first load — that mirrors the handoff which
  // shows `map+validate` (the failing node) selected by default.
  useEffect(() => {
    if (selectedService) return;
    const top = (data?.services ?? [])
      .slice()
      .sort((a, b) => b.error_trace_count - a.error_trace_count)[0];
    if (top && top.error_trace_count > 0) setSelectedService(top.service_name);
  }, [data, selectedService]);

  // Real health per member service (firing checks + open errors), so the
  // flow nodes read unhealthy even with no error traces in the window.
  const statusByService = useMemo(() => {
    const m: Record<string, ServiceStatus> = {};
    for (const s of data?.services ?? []) m[s.service_name] = s.status;
    return m;
  }, [data]);

  // Fetch detail + widgets for the selected service.
  useEffect(() => {
    if (!selectedService) {
      setServiceDetail(null);
      setServiceWidgets([]);
      return;
    }
    let cancelled = false;
    setServiceLoading(true);
    setServiceError(null);
    Promise.all([
      api.serviceDetail(selectedService, windowVal).catch((e) => {
        if (!cancelled) setServiceError(String(e.message ?? e));
        return null;
      }),
      api
        .serviceWidgets(selectedService, windowVal)
        // Integration drawer flattens widgets from every facet into a
        // single list — the drawer is a quick peek, not the full
        // per-facet dashboard you get on /services/{name}.
        .then((r: ServiceWidgetsResponse) =>
          r.facets.flatMap((f) => f.widgets)
        )
        .catch(() => []),
    ])
      .then(([detail, widgets]) => {
        if (cancelled) return;
        setServiceDetail(detail);
        setServiceWidgets(widgets);
      })
      .finally(() => {
        if (!cancelled) setServiceLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [selectedService, windowVal]);

  const onDelete = async () => {
    if (!confirm("Delete this integration? Matchers will be removed.")) return;
    try {
      await api.deleteIntegration(id);
      navigate("/integrations");
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  const integrationName = data?.integration.name;
  // On 404 the page header (which owns the breadcrumb leaf) isn't rendered,
  // so label the tab + breadcrumb "Not found" rather than the generic
  // "Integration". On valid pages this mirrors the header's own leaf value.
  const notFoundTitle = !data && !!error && /^404\b/.test(error);
  usePageTitle(notFoundTitle ? "Not found" : integrationName ?? "Integration");
  useBreadcrumbLeaf(notFoundTitle ? "Not found" : integrationName ?? null);

  const stats = useMemo(() => deriveStats(data), [data]);

  // First-run state: the integration matches no services this window AND
  // the flow has no historical nodes either — i.e. nothing has ever
  // flowed through it. Show the onboarding guide instead of an empty
  // graph + inspector.
  const showOnboarding =
    !!data && stats.serviceCount === 0 && flow !== null && flow.nodes.length === 0;

  // A 404 means the integration doesn't exist OR the caller can't see it —
  // render a plain not-found state rather than the page header (which would
  // otherwise leak the integration's name/title).
  if (notFoundTitle) {
    return (
      <div className="placeholder" style={{ marginTop: 64, textAlign: "center" }}>
        <h2 style={{ margin: 0 }}>Integration not found</h2>
        <p className="muted" style={{ marginTop: 8 }}>
          It doesn't exist, or you don't have access to it.
        </p>
        <Link to="/integrations" className="btn" style={{ marginTop: 16, display: "inline-block" }}>
          ← All integrations
        </Link>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <IntegrationPageHeader
        detail={data}
        actions={
          data && (
            // Runtime controls (pause/restart) intentionally omitted —
            // Sluicio is an observability tool, not a control plane.
            <div style={{ display: "flex", gap: 8 }}>
              {/* Mirrors the service page's "Edit service": jumps to the
                  integration's settings/edit view (tabs hidden there).
                  Hidden for viewers — they're read-only. */}
              {canWrite && (
                <Link className="btn primary" to={`/integrations/${encodeURIComponent(id)}/settings`}>
                  ✎ Edit integration
                </Link>
              )}
              {canDelete && (
                <button type="button" className="btn" onClick={onDelete}>
                  Delete
                </button>
              )}
            </div>
          )
        }
        belowStats={
          data && (
            <div className="mt-2 flex items-center gap-2 flex-wrap">
              <span
                className="muted"
                style={{ fontSize: 12, textTransform: "uppercase", letterSpacing: 0.5 }}
              >
                tags
              </span>
              {canWrite ? (
                <TagPicker
                  available={allTags}
                  selectedIds={(data.tags ?? []).map((t) => t.id)}
                  onChange={applyTagSelection}
                  onCreate={createTag}
                  placeholder={(data.tags ?? []).length === 0 ? "Add a tag…" : "+ tag"}
                />
              ) : (data.tags ?? []).length === 0 ? (
                <span className="muted" style={{ fontSize: 13 }}>none</span>
              ) : (
                <div style={{ display: "flex", flexWrap: "wrap", gap: 4 }}>
                  {(data.tags ?? []).map((t) => (
                    <TagChip key={t.id} tag={t} />
                  ))}
                </div>
              )}
              {tagsLoading && <span className="muted" style={{ fontSize: 12 }}>saving…</span>}
            </div>
          )
        }
      />

      {/* Sub-tab strip — Overview is the active tab on this page; the
          Messages tab routes to /integrations/:id/messages. The count
          suffix on Messages reflects total in-window traffic across
          this integration's services. */}
      <IntegrationTabs integrationId={id} messagesCount={stats.traces} errorsCount={integrationProblemCount(data)} />

      {error && <div className="alert alert--error">{error}</div>}
      {loading && !data && <div className="placeholder">Loading…</div>}

      {data && (
        <>
          {/* Metric strip */}
          <div className="grid grid-cols-2 gap-3 md:grid-cols-6">
            <MetricTile label="traffic (window)" value={formatNumber(stats.traces)} />
            <MetricTile
              label="success"
              value={`${stats.successPct.toFixed(1)}%`}
              tone={stats.successPct >= 99 ? "ok" : stats.successPct >= 95 ? "warn" : "err"}
            />
            <MetricTile
              label="error traces"
              value={formatNumber(stats.errors)}
              tone={stats.errors > 0 ? "err" : "default"}
              to={stats.errors > 0 ? `/integrations/${encodeURIComponent(id)}/errors` : undefined}
              title={stats.errors > 0 ? "See the error traces on the Errors tab" : undefined}
            />
            <MetricTile
              label="delayed (open)"
              value={delayedCount === null ? "—" : formatNumber(delayedCount)}
              tone={delayedCount && delayedCount > 0 ? "warn" : "default"}
              to={`/integrations/${encodeURIComponent(id)}/messages?delayed=1`}
              title="View delayed traces on the Messages tab"
            />
            <MetricTile label="services" value={String(stats.serviceCount)} />
            <MetricTile
              label="unhealthy"
              value={formatNumber(stats.unhealthy)}
              tone={stats.unhealthy > 0 ? "err" : "default"}
              to={stats.unhealthy > 0 ? `/integrations/${encodeURIComponent(id)}/errors` : undefined}
              title={stats.unhealthy > 0 ? "See the failing health checks on the Errors tab" : undefined}
            />
          </div>

          {/* Onboarding: a freshly-created integration has no services
              until matching telemetry arrives. Guide the user to send
              data + add matchers instead of showing an empty flow graph.
              Gated on an empty flow too, so an established-but-quiet
              integration (no traffic this window, but historical services)
              still shows its graph rather than the first-run guide. */}
          {showOnboarding && (
            <section
              className="rounded-lg border bg-surface-2"
              style={{ borderColor: "var(--border)" }}
            >
              <IntegrationServicesGuide integrationId={id} />
            </section>
          )}

          {/* Error breakdown — moved up here, directly below the stats, so
              what's wrong on this integration reads before the flow graph.
              User-defined metadata now lives on its own Metadata tab. */}
          {!showOnboarding && (
            <section
              className="rounded-lg border bg-surface-2 p-4"
              style={{ borderColor: "var(--border)" }}
            >
              <ErrorBreakdown
                integrationId={id}
                services={data.services ?? []}
                onJumpToService={(name) => setSelectedService(name)}
              />
            </section>
          )}

          {/* Main layout: graph + traffic + breakdown on the left, inspector
              on the right. Hidden in the first-run onboarding state, where the
              guide above replaces the (empty) graph. */}
          {!showOnboarding && (
          <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1fr_360px]">
            <div className="flex flex-col gap-4">
              <section
                className="overflow-hidden rounded-lg border bg-surface-2"
                style={{ borderColor: "var(--border)" }}
              >
                <div className="flex items-baseline justify-between border-b border-border px-4 py-3">
                  <div>
                    <h2 className="text-base font-semibold flex items-center gap-2">
                      Service flow
                      {flow?.historical && (
                        <span
                          className="rounded-full border px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide"
                          style={{
                            background: "var(--surface-3)",
                            color: "var(--ink-2)",
                            borderColor: "var(--border)",
                          }}
                          title="No traces in the selected range — showing services and hops discovered historically."
                        >
                          historical
                        </span>
                      )}
                    </h2>
                    <p className="text-xs text-muted">
                      {flow?.historical
                        ? "No traces in the selected range — showing services and hops discovered historically."
                        : "Click a service to inspect. Red borders mean a service is unhealthy (a failing health check)."}
                    </p>
                  </div>
                  <div className="text-xs text-muted">
                    {flow ? `${flow.nodes.length} services · ${flow.edges.length} hops` : ""}
                  </div>
                </div>
                <div style={{ height: 360 }}>
                  {flow ? (
                    <IntegrationFlow
                      nodes={flow.nodes}
                      edges={flow.edges}
                      selected={selectedService}
                      onSelect={(n) => setSelectedService(n || null)}
                      serviceSchemas={flow.service_schemas}
                      maps={flow.maps}
                      statusByService={statusByService}
                    />
                  ) : (
                    <div className="p-6 text-sm text-muted">Loading flow…</div>
                  )}
                </div>
                {flow?.maps && flow.maps.length > 0 && (
                  <div
                    className="border-t border-border px-4 py-3"
                    style={{ background: "var(--surface)" }}
                  >
                    <div className="text-[10px] uppercase tracking-wide text-muted">
                      Data shapes · maps
                    </div>
                    <div className="mt-1.5 flex flex-wrap gap-1.5">
                      {flow.maps.map((m) => (
                        <Link
                          key={m.id}
                          to="/maps"
                          className="rounded border px-2 py-0.5 text-xs hover:bg-surface-elevated"
                          style={{ borderColor: "var(--border)", color: "var(--ink-2)" }}
                          title={`Map "${m.name}"${
                            m.from_schema || m.to_schema
                              ? `: ${m.from_schema ?? "?"} → ${m.to_schema ?? "?"}`
                              : ""
                          }`}
                        >
                          <span className="font-medium">{m.name}</span>
                          {(m.from_schema || m.to_schema) && (
                            <span className="text-muted">
                              {" "}
                              · {m.from_schema ?? "?"} → {m.to_schema ?? "?"}
                            </span>
                          )}
                        </Link>
                      ))}
                    </div>
                  </div>
                )}
              </section>
            </div>

            <aside className="lg:sticky lg:top-20" style={{ alignSelf: "start", minHeight: 360 }}>
              <ServiceInspector
                serviceName={selectedService}
                detail={serviceDetail}
                widgets={serviceWidgets}
                loading={serviceLoading}
                error={serviceError}
              />
            </aside>
          </div>
          )}

          {/* The services list lives on the Services tab and matcher
              configuration on the Settings tab — the Overview stays the
              operational view. */}
        </>
      )}
    </div>
  );
}

interface MetricTileProps {
  label: string;
  value: string;
  tone?: "default" | "ok" | "warn" | "err";
  // When set, the tile becomes a link (e.g. the delayed tile → the
  // list of delayed traces) with a hover affordance.
  to?: string;
  title?: string;
}

function MetricTile({ label, value, tone = "default", to, title }: MetricTileProps) {
  const color = {
    default: "var(--ink)",
    ok: "var(--ok)",
    warn: "var(--warn)",
    err: "var(--err)",
  }[tone];
  const inner = (
    <>
      <div className="text-[10px] uppercase tracking-wide text-muted">{label}</div>
      <div className="mt-1 text-xl font-semibold tabular-nums" style={{ color }}>
        {value}
      </div>
    </>
  );
  if (to) {
    return (
      <Link
        to={to}
        title={title}
        className="block rounded-md border bg-surface-2 p-3 transition-colors hover:border-border-strong"
        style={{ borderColor: "var(--border)" }}
      >
        {inner}
      </Link>
    );
  }
  return (
    <div
      className="rounded-md border bg-surface-2 p-3"
      style={{ borderColor: "var(--border)" }}
    >
      {inner}
    </div>
  );
}

interface DerivedStats {
  traces: number;
  errors: number;
  delayed: number;
  successPct: number;
  serviceCount: number;
  unhealthy: number;
}

function deriveStats(data: IntegrationDetail | null): DerivedStats {
  if (!data) return { traces: 0, errors: 0, delayed: 0, successPct: 100, serviceCount: 0, unhealthy: 0 };
  // Prefer the backend's integration-level distinct counts (a trace that
  // spans two of the integration's services is counted once). Fall back
  // to summing per-service counts only if the field is absent (older API).
  const traces =
    data.message_count ?? (data.services ?? []).reduce((acc, s) => acc + s.trace_count, 0);
  const errors =
    data.error_message_count ??
    (data.services ?? []).reduce((acc, s) => acc + s.error_trace_count, 0);
  // Window-scoped delayed count (a missed-SLA failure, disjoint from
  // errors). This is consistent with the trace/error counts' window —
  // unlike the sticky "delayed (open)" firings tile, which is the
  // evaluator's lookback window.
  const delayed = data.delayed_message_count ?? 0;
  const successPct = traces > 0 ? (Math.max(0, traces - errors - delayed) / traces) * 100 : 100;
  const unhealthy = (data.services ?? []).filter(
    (s) => s.status === "errors" || s.status === "unhealthy",
  ).length;
  return {
    traces,
    errors,
    delayed,
    successPct,
    serviceCount: (data.services ?? []).length,
    unhealthy,
  };
}
