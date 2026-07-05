// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ServiceInspector — right-rail panel for the Integration detail
// "Graph + inspector" (variant B) layout. Renders the currently
// selected service's stats, throughput sparkline, recent errors,
// and a compact config snippet.
//
// Data: api.serviceDetail(name, window) + api.serviceWidgets(name,
// window) when available. The wireframe lists "in / out / success /
// p95 / errors 24h" which map to ServiceStats fields; throughput
// uses the existing throughput widget when present.

import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Sparkline, StatusPip, pipForStatus } from "./primitives";
import { api } from "../api/client";
import type {
  AlertInstance,
  AlertRule,
  ServiceDetailResponse,
  ServiceStatus,
  SpanSummary,
  TimePointData,
  WidgetResult,
} from "../api/types";
import { formatDurationMs, formatNumber } from "../lib/format";
import { alertCondition, alertSignalLabel } from "../lib/alertRule";

interface Props {
  serviceName: string | null;
  detail: ServiceDetailResponse | null;
  widgets: WidgetResult[];
  loading: boolean;
  error: string | null;
}

export default function ServiceInspector({
  serviceName,
  detail,
  widgets,
  loading,
  error,
}: Props) {
  // The service's currently-failing health checks (firing rules bound to
  // it), so an unhealthy service explains *why* — not just "0 errors".
  const [failingChecks, setFailingChecks] = useState<{ rule: AlertRule; inst: AlertInstance }[]>([]);
  useEffect(() => {
    if (!serviceName) {
      setFailingChecks([]);
      return;
    }
    let cancelled = false;
    Promise.all([api.listAlertRules({ service: serviceName }), api.listAlertInstances(200)])
      .then(([rr, ii]) => {
        if (cancelled) return;
        const firing = new Map<string, AlertInstance>();
        for (const i of ii.instances ?? []) if (i.state === "firing") firing.set(i.alert_rule_id, i);
        setFailingChecks(
          (rr.rules ?? [])
            .filter((r) => firing.has(r.id))
            .map((r) => ({ rule: r, inst: firing.get(r.id)! })),
        );
      })
      .catch(() => {
        if (!cancelled) setFailingChecks([]);
      });
    return () => {
      cancelled = true;
    };
  }, [serviceName]);

  if (!serviceName) {
    return (
      <div
        className="flex h-full items-center justify-center rounded-lg border border-dashed p-6 text-sm text-muted"
        style={{ borderColor: "var(--border)" }}
      >
        Click a service in the flow to inspect.
      </div>
    );
  }

  const stats = detail?.stats;
  const recentErrors: SpanSummary[] =
    detail?.recent_spans.filter((s) => s.status_code === "Error").slice(0, 5) ?? [];
  const throughputWidget = widgets.find((w) => w.kind === "throughput");
  const throughputSeries =
    throughputWidget && Array.isArray(throughputWidget.data)
      ? (throughputWidget.data as TimePointData[]).map((p) => p.value)
      : undefined;

  // Health is driven solely by configured health checks (backend status):
  // a service is unhealthy iff a check is firing. Raw trace errors no longer
  // flip it red, so we never fabricate an error status from the window rate.
  const status: ServiceStatus = detail?.status ?? "ok";

  return (
    <div
      className="flex h-full flex-col gap-4 overflow-auto rounded-xl border p-4 shadow-sm"
      style={{
        background: "var(--primary-soft)",
        borderColor: "color-mix(in oklab, var(--primary) 25%, transparent)",
        color: "var(--primary-ink)",
      }}
    >
      <div>
        <div
          className="text-[11px] uppercase tracking-wide"
          style={{ color: "var(--primary-ink)", opacity: 0.7 }}
        >
          selected service
        </div>
        <div className="mt-1 flex items-center gap-2">
          <Link
            to={`/services/${encodeURIComponent(serviceName)}`}
            className="text-xl font-semibold hover:underline"
          >
            {serviceName}
          </Link>
          <StatusPip kind={pipForStatus(status)} />
        </div>
        {detail?.service_namespace && (
          <div className="font-mono text-xs text-muted">{detail.service_namespace}</div>
        )}
      </div>

      {error && <div className="alert alert--error">{error}</div>}
      {loading && !detail && <div className="text-sm text-muted">Loading…</div>}

      {/* Why unhealthy — the failing health checks. Health is check-driven
          only, so this lists the firing checks (a metric/log/trace check can
          fail with zero in-window error traces); raw errors don't appear. */}
      {status === "unhealthy" && failingChecks.length > 0 && (
          <div
            className="rounded-md p-3"
            style={{
              borderLeft: "3px solid var(--err)",
              background: "var(--err-soft)",
              color: "var(--err-ink)",
              // overflow-wrap is inherited, so this wraps every long token
              // inside (rule names, metric conditions) — no horizontal spill.
              overflowWrap: "anywhere",
            }}
          >
            <div className="text-xs font-semibold" style={{ marginBottom: 6 }}>
              Why unhealthy
            </div>
            {failingChecks.map(({ rule }) => (
              <div key={rule.id} style={{ marginTop: 4 }}>
                <div style={{ display: "flex", gap: 6, alignItems: "center", flexWrap: "wrap" }}>
                  <span className="text-sm font-medium">{rule.name}</span>
                  {alertSignalLabel(rule.signal) && (
                    <span className="m-rule-badge">{alertSignalLabel(rule.signal)}</span>
                  )}
                </div>
                {/* On its own line + break-word so a long metric condition
                    (with attribute scope) wraps inside the box. */}
                <div
                  className="font-mono"
                  style={{ fontSize: 11, opacity: 0.85, wordBreak: "break-word", whiteSpace: "normal" }}
                >
                  {alertCondition(rule)}
                </div>
              </div>
            ))}
            <Link
              to={`/services/${encodeURIComponent(serviceName)}?tab=health`}
              className="text-sm underline-offset-2 hover:underline"
              style={{ color: "var(--err-ink)", display: "inline-block", marginTop: 6 }}
            >
              View health checks →
            </Link>
          </div>
        )}

      {stats && (
        <>
          <div className="grid grid-cols-2 gap-3">
            <StatTile
              label="traces"
              value={formatNumber(stats.trace_count)}
            />
            <StatTile
              label="success"
              value={`${((1 - stats.error_rate) * 100).toFixed(1)}%`}
              tone={stats.error_rate > 0.05 ? "err" : "ok"}
            />
            <StatTile
              label="p95"
              value={formatDurationMs(stats.p95_duration_ms)}
            />
            <StatTile
              label="error traces"
              value={formatNumber(stats.error_trace_count)}
              tone={stats.error_trace_count > 0 ? "err" : "default"}
            />
          </div>

          <div>
            <div className="mb-1 text-xs text-muted">throughput · window</div>
            <Sparkline
              data={throughputSeries}
              seed={hashString(serviceName) || 1}
              width={300}
              height={56}
              tone="default"
            />
          </div>

          <div>
            <div className="mb-1 text-xs text-muted">recent error traces</div>
            {recentErrors.length === 0 ? (
              <div className="text-sm text-muted">No error traces in this window.</div>
            ) : (
              <ul className="space-y-1 text-sm">
                {recentErrors.map((s) => (
                  <li key={s.span_id} className="flex items-start gap-2">
                    <span
                      className="mt-1 inline-block h-1.5 w-1.5 rounded-full"
                      style={{ background: "var(--err)" }}
                    />
                    <span className="font-mono text-xs text-muted">
                      {timeShort(s.timestamp)}
                    </span>
                    <span className="truncate">
                      <span className="font-medium">{s.span_name}</span>
                      {s.status_message && (
                        <span className="text-muted"> · {s.status_message}</span>
                      )}
                    </span>
                  </li>
                ))}
                {detail && detail.recent_spans.length > recentErrors.length && (
                  <li>
                    <Link
                      to={`/services/${encodeURIComponent(serviceName)}`}
                      className="text-sm underline-offset-2 hover:underline"
                      style={{ color: "var(--primary)" }}
                    >
                      see all errors →
                    </Link>
                  </li>
                )}
              </ul>
            )}
          </div>
        </>
      )}

      <div>
        <div className="mb-1 text-xs text-muted">integrations</div>
        {detail && detail.integrations.length > 0 ? (
          <div className="flex flex-wrap gap-1">
            {detail.integrations.map((i) => (
              <Link key={i.id} className="badge" to={`/integrations/${i.id}`}>
                {i.name}
              </Link>
            ))}
          </div>
        ) : (
          <div className="text-sm text-muted">—</div>
        )}
      </div>
    </div>
  );
}

interface StatProps {
  label: string;
  value: string;
  tone?: "default" | "ok" | "warn" | "err";
}

function StatTile({ label, value, tone = "default" }: StatProps) {
  const color = {
    default: "var(--ink)",
    ok: "var(--ok)",
    warn: "var(--warn)",
    err: "var(--err)",
  }[tone];
  return (
    <div
      className="rounded-md border p-2"
      style={{ borderColor: "var(--border)", background: "var(--surface-2)" }}
    >
      <div
        className="text-[10px] uppercase tracking-wide"
        style={{ color: "var(--muted)" }}
      >
        {label}
      </div>
      <div className="mt-0.5 text-lg font-semibold tabular-nums" style={{ color }}>
        {value}
      </div>
    </div>
  );
}

function timeShort(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

function hashString(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) {
    h = (h << 5) - h + s.charCodeAt(i);
    h |= 0;
  }
  return Math.abs(h);
}
