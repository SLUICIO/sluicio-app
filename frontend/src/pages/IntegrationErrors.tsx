// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationErrors — the Errors tab on an integration detail page,
// mounted at /integrations/:id/errors. The single "what's wrong right
// now" triage view. An integration fails in three distinct ways, and an
// operator needs all three at a glance (they're the three "bad" tiles on
// the Overview: errors / delayed / unhealthy):
//
//   1. Failing health checks — a configured threshold/SLA is breaching
//      on one of the integration's services (or the integration itself).
//   2. Failed traces        — traces that carry an error span.
//   3. Delayed traces       — traces that missed a trace-completion SLA
//      (an open firing).
//
// Each is its own section with a count, drill-downs (a check → its
// service; a trace → the trace blade), and an all-clear empty state.
// When all three are clear the page says so plainly.

import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import AlertInstanceActions from "../components/AlertInstanceActions";
import IntegrationPageHeader from "../components/IntegrationPageHeader";
import IntegrationTabs from "../components/IntegrationTabs";
import { integrationProblemCount } from "../lib/integrationHealth";
import TraceDrawer from "../components/TraceDrawer";
import { StatusPip } from "../components/primitives";
import { useCurrentUser } from "../lib/useCurrentUser";
import type {
  FailingCheck,
  IntegrationDetail,
  OpenServiceError,
  TraceCompletionFiring,
  TraceSearchResult,
} from "../api/types";
import { formatDateTime, formatNumber, formatRelative } from "../lib/format";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";
import { useInstanceHighlight } from "../lib/useInstanceHighlight";

// How many trace rows to preview per section. The headline counts come
// from the server (window-accurate); these lists are a recent sample
// with a "view all" link to the Messages tab for the full set.
const TRACE_PREVIEW = 15;

type Severity = "info" | "warning" | "critical";

function sevPip(sev: string): "err" | "warn" | "ok" {
  return sev === "critical" ? "err" : sev === "warning" ? "warn" : "ok";
}

export default function IntegrationErrors() {
  const { id = "" } = useParams();
  const [windowVal] = useTimeWindow();
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");
  // Notification deep links land here with ?instance=<id> — pulse the row.
  const highlight = useInstanceHighlight();

  const [detail, setDetail] = useState<IntegrationDetail | null>(null);
  const [checks, setChecks] = useState<FailingCheck[]>([]);
  const [openErrors, setOpenErrors] = useState<OpenServiceError[]>([]);
  const [firings, setFirings] = useState<TraceCompletionFiring[]>([]);
  const [errorTraces, setErrorTraces] = useState<TraceSearchResult[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [openTraceId, setOpenTraceId] = useState<string | null>(null);
  // Bumped after acknowledging/resolving a check so the feed + the
  // integration header (its status may flip back to healthy on resolve)
  // re-fetch.
  const [bump, setBump] = useState(0);

  usePageTitle(detail ? `${detail.integration.name} · Errors` : "Integration errors");

  // The integration detail + its delayed-trace firings only need the id.
  useEffect(() => {
    if (!id) return;
    let cancelled = false;
    setLoading(true);
    setError(null);
    api
      .getIntegration(id, windowVal)
      .then((d) => !cancelled && setDetail(d))
      .catch((e) => !cancelled && setError(String(e.message ?? e)))
      .finally(() => !cancelled && setLoading(false));
    api
      .listCompletionFirings(id, windowVal)
      .then((r) => !cancelled && setFirings(r.firings ?? []))
      .catch(() => {
        /* non-fatal: section just shows nothing */
      });
    return () => {
      cancelled = true;
    };
  }, [id, windowVal, bump]);

  // Failing checks (scoped to this integration) + a recent error-trace
  // sample need the integration's name + service set, so they wait for
  // the detail to land.
  useEffect(() => {
    if (!detail) return;
    let cancelled = false;
    const name = detail.integration.name;
    const svcSet = new Set((detail.services ?? []).map((s) => s.service_name));
    api
      .errorsFeed(windowVal)
      .then((f) => {
        if (cancelled) return;
        // Keep acknowledged (handled_at set) checks visible too — they're
        // still firing, just being worked on; the row tags them and shows
        // only a Resolve action. Resolving drops them (no longer firing).
        setChecks(
          (f.failing_checks ?? []).filter(
            (c) =>
              c.integration_id === id ||
              (c.target_kind === "service" && !!c.service_name && svcSet.has(c.service_name)),
          ),
        );
        // Persisted unacknowledged errors for this integration's services.
        setOpenErrors((f.open_errors ?? []).filter((e) => svcSet.has(e.service_name)));
      })
      .catch(() => {
        /* non-fatal */
      });
    api
      .searchMessages({
        range: windowVal,
        filters: [{ field: "integration", op: "is", value: name }],
        limit: 100,
      })
      .then((r) => {
        if (!cancelled) setErrorTraces((r.results ?? []).filter((t) => t.has_error).slice(0, TRACE_PREVIEW));
      })
      .catch(() => {
        /* non-fatal */
      });
    return () => {
      cancelled = true;
    };
  }, [detail, id, windowVal, bump]);

  // Open (firing, unhandled) trace-completion firings, deduped to one row
  // per trace keeping the highest severity + earliest start.
  const delayed = useMemo(() => {
    const rank = (s: string) => (s === "critical" ? 3 : s === "warning" ? 2 : s === "info" ? 1 : 0);
    const byTrace = new Map<string, { traceId: string; rule: string; severity: Severity; since: string }>();
    for (const f of firings) {
      if (f.state !== "firing" || f.handled_at) continue;
      const prev = byTrace.get(f.trace_id);
      if (!prev || rank(f.severity) > rank(prev.severity)) {
        byTrace.set(f.trace_id, {
          traceId: f.trace_id,
          rule: f.rule_name,
          severity: f.severity,
          since: f.started_at,
        });
      }
    }
    return [...byTrace.values()];
  }, [firings]);

  // Headline counts: server-computed window totals where we have them, so
  // the section count is accurate even when the preview list is capped.
  const failedCount = detail?.error_message_count ?? 0;
  const delayedCount = detail?.delayed_message_count ?? delayed.length;
  const checksCount = checks.length;
  const openCount = openErrors.length;
  const allClear = checksCount === 0 && openCount === 0 && failedCount === 0 && delayedCount === 0;

  return (
    <div className="flex flex-col gap-6">
      <IntegrationPageHeader detail={detail} />
      <IntegrationTabs integrationId={id} errorsCount={integrationProblemCount(detail)} />

      {error && <div className="alert alert--error">{error}</div>}
      {loading && !detail && <div className="placeholder">Loading…</div>}

      {detail && (
        <>
          {/* Summary strip — the failure modes at a glance. */}
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
            <SummaryTile label="unacknowledged errors" value={openCount} tone={openCount > 0 ? "err" : "ok"} />
            <SummaryTile label="failing health checks" value={checksCount} tone={checksCount > 0 ? "err" : "ok"} />
            <SummaryTile label="failed traces" value={failedCount} tone={failedCount > 0 ? "err" : "ok"} />
            <SummaryTile label="delayed traces" value={delayedCount} tone={delayedCount > 0 ? "warn" : "ok"} />
          </div>

          {allClear ? (
            <section
              className="rounded-lg border bg-surface-2 p-6 text-center"
              style={{ borderColor: "var(--border)" }}
            >
              <div className="text-base font-semibold" style={{ color: "var(--ok)" }}>
                All clear
              </div>
              <p className="mt-1 text-sm text-muted">
                No failing health checks, failed traces, or delayed traces in this window.
              </p>
            </section>
          ) : (
            <>
              {/* 0 — Unacknowledged errors (persisted until acknowledged) */}
              <Section
                title="Unacknowledged errors"
                count={openCount}
                subtitle="Services that have produced errors since they were last cleared. These persist regardless of the time window until acknowledged."
                empty="No unacknowledged errors."
              >
                {openErrors.map((e) => (
                  <OpenErrorRow
                    key={e.service_name}
                    err={e}
                    canWrite={canWrite}
                    onTrace={setOpenTraceId}
                    onChanged={() => setBump((x) => x + 1)}
                    onError={setError}
                  />
                ))}
              </Section>

              {/* 1 — Failing health checks */}
              <Section
                title="Failing health checks"
                count={checksCount}
                subtitle="Configured thresholds currently breaching on this integration's services."
                empty="No health checks are failing."
              >
                {checks.map((c) => (
                  <div
                    key={c.id}
                    {...highlight.props(c.id, "grid items-center gap-3 border-t border-border px-4 py-2.5 text-sm first:border-t-0")}
                    style={{ gridTemplateColumns: "20px 1fr 150px 120px auto" }}
                  >
                    <StatusPip kind={sevPip(c.severity)} />
                    <div className="min-w-0">
                      <div className="truncate font-medium">
                        {c.rule_name}
                        {c.handled_at && (
                          <span className="badge" style={{ marginLeft: 8, fontSize: 10 }} title={`Acknowledged ${formatRelative(c.handled_at)}`}>
                            acknowledged
                          </span>
                        )}
                      </div>
                      {c.summary && <div className="truncate text-xs text-muted">{c.summary}</div>}
                    </div>
                    <div className="truncate text-xs">
                      {c.service_name ? (
                        <Link
                          to={`/services/${encodeURIComponent(c.service_name)}`}
                          className="hover:underline"
                          style={{ color: "var(--primary)" }}
                        >
                          {c.service_name} →
                        </Link>
                      ) : (
                        <span className="text-muted">{c.target_kind === "global" ? "org-wide" : "integration"}</span>
                      )}
                    </div>
                    <div className="text-right text-xs text-muted">
                      <span className={`badge sev-${c.severity}`}>{c.severity}</span>
                      <div className="mt-0.5">since {formatRelative(c.started_at)}</div>
                    </div>
                    <div className="text-right">
                      {canWrite ? (
                        <AlertInstanceActions
                          instanceId={c.id}
                          acknowledged={!!c.handled_at}
                          onChanged={() => setBump((x) => x + 1)}
                          onError={setError}
                        />
                      ) : null}
                    </div>
                  </div>
                ))}
              </Section>

              {/* 2 — Failed traces */}
              <Section
                title="Failed traces"
                count={failedCount}
                subtitle="Traces with an error span on one of this integration's services."
                empty="No failed traces."
                footer={
                  failedCount > 0 ? (
                    <Link
                      to={`/integrations/${encodeURIComponent(id)}/messages?s=${encodeURIComponent("err only")}`}
                      className="text-xs hover:underline"
                      style={{ color: "var(--primary)" }}
                    >
                      View all in Messages →
                    </Link>
                  ) : undefined
                }
              >
                {errorTraces.map((t) => (
                  <button
                    type="button"
                    key={t.trace_id}
                    onClick={() => setOpenTraceId(t.trace_id)}
                    className="grid w-full items-center gap-3 border-t border-border px-4 py-2.5 text-left text-sm first:border-t-0 hover:bg-surface-3"
                    style={{ gridTemplateColumns: "20px 150px 1fr 130px" }}
                  >
                    <StatusPip kind="err" />
                    <span className="truncate font-mono text-xs" style={{ color: "var(--primary)" }}>
                      {t.trace_id.slice(0, 16)}…
                    </span>
                    <span className="truncate">
                      {t.matched_service}
                      <span className="text-muted"> · {t.matched_span_name}</span>
                    </span>
                    <span className="text-right text-xs text-muted">
                      {formatDateTime(t.trace_start)}
                    </span>
                  </button>
                ))}
                {failedCount > 0 && errorTraces.length === 0 && (
                  <div className="px-4 py-3 text-sm text-muted">
                    {formatNumber(failedCount)} failed trace{failedCount === 1 ? "" : "s"} in this window — view them on the Messages tab.
                  </div>
                )}
              </Section>

              {/* 3 — Delayed traces */}
              <Section
                title="Delayed traces"
                count={delayedCount}
                subtitle="Traces that breached a trace-completion SLA and haven't been handled."
                empty="No delayed traces."
                footer={
                  delayedCount > 0 ? (
                    <Link
                      to={`/integrations/${encodeURIComponent(id)}/messages?delayed=1`}
                      className="text-xs hover:underline"
                      style={{ color: "var(--primary)" }}
                    >
                      View all delayed traces →
                    </Link>
                  ) : undefined
                }
              >
                {delayed.map((d) => (
                  <button
                    type="button"
                    key={d.traceId}
                    onClick={() => setOpenTraceId(d.traceId)}
                    className="grid w-full items-center gap-3 border-t border-border px-4 py-2.5 text-left text-sm first:border-t-0 hover:bg-surface-3"
                    style={{ gridTemplateColumns: "20px 150px 1fr 130px" }}
                  >
                    <StatusPip kind={sevPip(d.severity)} />
                    <span className="truncate font-mono text-xs" style={{ color: "var(--primary)" }}>
                      {d.traceId.slice(0, 16)}…
                    </span>
                    <span className="truncate text-muted">{d.rule}</span>
                    <span className="text-right text-xs text-muted">since {formatRelative(d.since)}</span>
                  </button>
                ))}
                {delayedCount > 0 && delayed.length === 0 && (
                  <div className="px-4 py-3 text-sm text-muted">
                    {formatNumber(delayedCount)} delayed trace{delayedCount === 1 ? "" : "s"} in this window — view them on the Messages tab.
                  </div>
                )}
              </Section>
            </>
          )}
        </>
      )}

      <TraceDrawer traceId={openTraceId} onClose={() => setOpenTraceId(null)} integrationContextId={id} />
    </div>
  );
}

// OpenErrorRow — a persisted unacknowledged error for one of the
// integration's services. Acknowledge clears that service's errors (bumps
// the watermark); the row drops off until new errors arrive after that.
function OpenErrorRow({
  err,
  canWrite,
  onTrace,
  onChanged,
  onError,
}: {
  err: OpenServiceError;
  canWrite: boolean;
  onTrace: (traceId: string) => void;
  onChanged: () => void;
  onError: (msg: string) => void;
}) {
  const [busy, setBusy] = useState(false);
  const acknowledge = async () => {
    if (!window.confirm(`Acknowledge ${formatNumber(err.error_traces)} error trace${err.error_traces === 1 ? "" : "s"} on ${err.service_name}? New error traces after this re-open it.`)) {
      return;
    }
    setBusy(true);
    try {
      await api.clearServiceErrors(err.service_name, "Acknowledged from Errors");
      onChanged();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <div
      className="grid items-center gap-3 border-t border-border px-4 py-2.5 text-sm first:border-t-0"
      style={{ gridTemplateColumns: "20px 1fr 150px 120px" }}
    >
      <StatusPip kind="err" />
      <div className="min-w-0">
        <Link to={`/services/${encodeURIComponent(err.service_name)}`} className="truncate font-medium hover:underline" style={{ color: "var(--primary)" }}>
          {err.service_name}
        </Link>
        <div className="text-xs text-muted">
          {formatNumber(err.error_traces)} unacknowledged error trace{err.error_traces === 1 ? "" : "s"} · since {formatRelative(err.first_error_at)}
        </div>
      </div>
      <div className="truncate text-xs">
        {err.sample_trace_id ? (
          <button
            type="button"
            onClick={() => onTrace(err.sample_trace_id!)}
            className="hover:underline"
            style={{ color: "var(--primary)" }}
          >
            latest {formatRelative(err.last_error_at)} →
          </button>
        ) : (
          <span className="text-muted">latest {formatRelative(err.last_error_at)}</span>
        )}
      </div>
      <div className="text-right">
        {canWrite && (
          <button type="button" className="btn btn--sm" disabled={busy} onClick={acknowledge}>
            Acknowledge
          </button>
        )}
      </div>
    </div>
  );
}

function SummaryTile({
  label,
  value,
  tone,
}: {
  label: string;
  value: number;
  tone: "ok" | "warn" | "err";
}) {
  const color = { ok: "var(--ok)", warn: "var(--warn)", err: "var(--err)" }[tone];
  return (
    <div className="rounded-md border bg-surface-2 p-3" style={{ borderColor: "var(--border)" }}>
      <div className="text-[10px] uppercase tracking-wide text-muted">{label}</div>
      <div className="mt-1 text-xl font-semibold tabular-nums" style={{ color: value > 0 ? color : "var(--ink)" }}>
        {formatNumber(value)}
      </div>
    </div>
  );
}

function Section({
  title,
  count,
  subtitle,
  empty,
  footer,
  children,
}: {
  title: string;
  count: number;
  subtitle: string;
  empty: string;
  footer?: React.ReactNode;
  children: React.ReactNode;
}) {
  const hasRows = Array.isArray(children) ? children.flat().filter(Boolean).length > 0 : !!children;
  return (
    <section className="overflow-hidden rounded-lg border bg-surface-2" style={{ borderColor: "var(--border)" }}>
      <div className="flex items-baseline justify-between gap-3 border-b border-border px-4 py-3">
        <div className="min-w-0">
          <h2 className="text-base font-semibold">
            {title} <span className="text-muted">· {formatNumber(count)}</span>
          </h2>
          <p className="text-xs text-muted">{subtitle}</p>
        </div>
        {footer}
      </div>
      {count === 0 ? (
        <div className="px-4 py-4 text-sm text-muted">{empty}</div>
      ) : hasRows ? (
        children
      ) : (
        // count > 0 but no preview rows loaded — children handle the fallback line.
        children
      )}
    </section>
  );
}
