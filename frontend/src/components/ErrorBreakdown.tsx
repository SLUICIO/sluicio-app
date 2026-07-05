// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ErrorBreakdown — answers the handoff's central "error attribution"
// question for the Integration detail page: *where* in the pipeline
// are failures originating, and *what* error is dominating? The
// component renders a hero callout restating the top failure in
// plain English, followed by ranked per-service breakdown rows with
// horizontal bars.
//
// Data: derived client-side from the per-service ServiceSummary list
// the IntegrationDetail page already fetches. The dominant cause
// would ideally come from a backend aggregation endpoint (the
// handoff explicitly suggests one) — for now we surface the per-
// service error counts and let the inspector show the underlying
// span messages.

import { useState } from "react";
import { Link } from "react-router-dom";
import type { ServiceSummary } from "../api/types";
import { formatNumber } from "../lib/format";
import { useCurrentUser } from "../lib/useCurrentUser";
import CreateTraceAlertDrawer from "./CreateTraceAlertDrawer";

// The Messages tab understands ?s=<status>; "err only" pre-filters the
// integration's message list to failed traces.
const ERRORS_ONLY_QUERY = `?s=${encodeURIComponent("err only")}`;

interface Breakdown {
  service_name: string;
  type?: string;
  errors: number;
  pct: number;
}

interface Props {
  integrationId: string;
  services: ServiceSummary[];
  onJumpToService?: (serviceName: string) => void;
}

export default function ErrorBreakdown({
  integrationId,
  services,
  onJumpToService,
}: Props) {
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");
  const [showAlert, setShowAlert] = useState(false);
  const total = services.reduce((acc, s) => acc + s.error_trace_count, 0);
  if (total === 0) {
    // No error traces in the window — but a service can still be unhealthy
    // from a firing health check (metric/log) with zero error traces. Say
    // so instead of an all-clear, and point at the failing service.
    const unhealthy = services.filter((s) => s.status === "unhealthy" || s.status === "errors");
    if (unhealthy.length > 0) {
      return (
        <div
          className="rounded-md p-4 text-sm"
          style={{ borderLeft: "4px solid var(--err)", background: "var(--err-soft)", color: "var(--err-ink)" }}
        >
          <div className="font-semibold">
            No error traces in this window, but {unhealthy.length} service
            {unhealthy.length === 1 ? " is" : "s are"} unhealthy.
          </div>
          <div className="mt-1" style={{ opacity: 0.85 }}>
            A failing health check (metric or log) can flip a service unhealthy without producing
            error traces. Open the service to see which check is failing:
          </div>
          <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1">
            {unhealthy.map((s) => (
              <Link
                key={s.service_name}
                to={`/services/${encodeURIComponent(s.service_name)}?tab=health`}
                className="font-medium underline-offset-2 hover:underline"
                style={{ color: "var(--err-ink)" }}
              >
                {s.service_name} →
              </Link>
            ))}
          </div>
        </div>
      );
    }
    return (
      <div
        className="rounded-md border p-4 text-sm text-muted"
        style={{ borderColor: "var(--border)" }}
      >
        No error traces in this window. 🎉
      </div>
    );
  }

  const breakdowns: Breakdown[] = services
    .filter((s) => s.error_trace_count > 0)
    .map((s) => ({
      service_name: s.service_name,
      // Pick the first non-core facet as a compact "kind" label —
      // mirrors what the old single ServiceType slot used to give us.
      type: s.service_facets?.find((f) => f.slug !== "core")?.name,
      errors: s.error_trace_count,
      pct: (s.error_trace_count / total) * 100,
    }))
    .sort((a, b) => b.errors - a.errors);

  const top = breakdowns[0];

  return (
    <div className="space-y-3">
      <div className="flex items-baseline justify-between">
        <div>
          <h3 className="text-lg font-semibold">Where are the error traces?</h3>
          <p className="text-xs text-muted">
            {total} error trace{total === 1 ? "" : "s"} across {breakdowns.length} service
            {breakdowns.length === 1 ? "" : "s"}
          </p>
        </div>
      </div>

      {/* Hero callout — restate the dominant failure in plain English */}
      <div
        className="rounded-xl p-3"
        style={{
          borderLeft: "4px solid var(--err)",
          background: "var(--err-soft)",
          color: "var(--err-ink)",
        }}
      >
        <div className="text-base leading-snug">
          <span className="font-semibold">{top.pct.toFixed(0)}% of failures</span>{" "}
          come from{" "}
          {top.service_name ? (
            // Link straight to the service's own page. (A click used to just
            // re-select the right-rail inspector, but the page auto-selects
            // the top error service on load — so clicking the service the
            // callout already names did nothing.)
            <Link
              to={`/services/${encodeURIComponent(top.service_name)}`}
              className="font-semibold underline underline-offset-2 hover:no-underline"
              style={{ color: "var(--err-ink)" }}
            >
              {top.service_name}
            </Link>
          ) : (
            <span className="font-semibold">an unnamed service</span>
          )}
          {top.type && (
            <span style={{ color: "color-mix(in oklab, var(--err-ink) 70%, transparent)" }}>
              {" "}
              · {top.type}
            </span>
          )}
          .
        </div>
        <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-sm">
          {/* Failed traces for this integration, on the Messages tab
              pre-filtered to errors (the tab is already scoped to the
              integration). */}
          <Link
            to={`/integrations/${encodeURIComponent(integrationId)}/messages${ERRORS_ONLY_QUERY}`}
            className="font-medium underline-offset-2 hover:underline"
            style={{ color: "var(--err-ink)" }}
          >
            see all {formatNumber(total)} failed →
          </Link>
          {canWrite && (
            <button
              type="button"
              onClick={() => setShowAlert(true)}
              className="font-medium underline-offset-2 hover:underline"
              style={{ color: "var(--err-ink)" }}
            >
              create alert rule
            </button>
          )}
        </div>
      </div>

      {showAlert && (
        <CreateTraceAlertDrawer
          integrationId={integrationId}
          onClose={() => setShowAlert(false)}
        />
      )}

      {/* Breakdown rows */}
      <div className="space-y-2">
        {breakdowns.map((b, i) => (
          <BreakdownRow
            key={b.service_name}
            b={b}
            dominant={i === 0}
            onClick={() => onJumpToService?.(b.service_name)}
          />
        ))}
      </div>
    </div>
  );
}

interface RowProps {
  b: Breakdown;
  dominant: boolean;
  onClick: () => void;
}

// (CreateTraceAlertDrawer now lives in its own module so the service
// Traces tab can reuse it with a service scope. See
// ./CreateTraceAlertDrawer.tsx.)

function BreakdownRow({ b, dominant, onClick }: RowProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="grid w-full grid-cols-[180px_60px_1fr_50px] items-center gap-3 rounded-md px-1 py-1 text-left text-sm transition-colors hover:bg-surface-elevated"
    >
      <div className="min-w-0">
        <div className="truncate font-medium">{b.service_name}</div>
        {b.type && <div className="text-xs text-muted">{b.type}</div>}
      </div>
      <div className="text-lg font-semibold tabular-nums">{b.pct.toFixed(0)}%</div>
      <div
        className="relative h-4 overflow-hidden rounded-sm border"
        style={{
          borderColor: "var(--border)",
          background: "var(--surface-3)",
        }}
      >
        <div
          className="h-full"
          style={{
            width: `${b.pct}%`,
            background: dominant
              ? "var(--err)"
              : "color-mix(in oklab, var(--err) 35%, transparent)",
          }}
        />
      </div>
      <div className="text-right tabular-nums">{formatNumber(b.errors)}</div>
    </button>
  );
}
