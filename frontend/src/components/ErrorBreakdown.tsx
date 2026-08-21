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

import { useEffect, useState } from "react";
import ServiceRef from "./ServiceRef";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { ErrorBreakdownResponse, ServiceSummary } from "../api/types";
import { formatNumber } from "../lib/format";
import { useCurrentUser } from "../lib/useCurrentUser";
import CreateTraceAlertDrawer from "./CreateTraceAlertDrawer";

// The Messages tab understands ?s=<status>; "err only" pre-filters the
// integration's message list to failed traces.
const ERRORS_ONLY_QUERY = `?s=${encodeURIComponent("err only")}`;

interface Breakdown {
  service_name: string;
  /** Every matching facet name, `core` excluded. Often more than one. */
  facets: string[];
  errors: number;
  pct: number;
}

interface Props {
  integrationId: string;
  services: ServiceSummary[];
  onJumpToService?: (serviceName: string) => void;
  /** Time window, so the attribution matches the rest of the page. */
  window?: string;
}

export default function ErrorBreakdown({
  integrationId,
  services,
  onJumpToService,
  window: windowVal = "1h",
}: Props) {
  const { can } = useCurrentUser();
  const canWrite = can("integration.write");
  const [showAlert, setShowAlert] = useState(false);
  // Server-side attribution (issue #12). Only used when it reports a
  // dimension other than "service"; the per-service view below is still
  // the right answer for an integration that spans several services,
  // and re-deriving it here would be a second source of truth for a
  // number already on the page.
  const [attribution, setAttribution] = useState<ErrorBreakdownResponse | null>(null);
  useEffect(() => {
    let cancelled = false;
    api
      .integrationErrorBreakdown(integrationId, windowVal)
      .then((r) => !cancelled && setAttribution(r))
      .catch(() => !cancelled && setAttribution(null));
    return () => {
      cancelled = true;
    };
  }, [integrationId, windowVal]);
  const total = services.reduce((acc, s) => acc + s.error_trace_count, 0);
  // Computed BEFORE the "no error traces" early return below, which
  // sums per-service counts that respect the acknowledgement watermark.
  // The page header counts raw failing traces in the window, so an
  // integration whose errors were acknowledged showed "3 error traces"
  // at the top and "No error traces 🎉" immediately underneath. The
  // server attribution agrees with the header, so it has to be able to
  // speak before the client-side sum declares the all-clear.
  // One service carrying this integration: "100% of failures come from
  // <the runtime>" is true and useless, so the server splits the
  // failures by the defining attribute or by the operation instead.
  const byDimension =
    attribution && attribution.dimension !== "service" && attribution.buckets.length > 0
      ? attribution
      : null;

  if (byDimension) {
    const max = Math.max(...byDimension.buckets.map((b) => b.errors), 1);
    const top = byDimension.buckets[0];
    const label = byDimension.dimension === "attribute" ? byDimension.attribute_key : "operation";
    return (
      <div className="space-y-3">
        <div>
          <h3 className="text-lg font-semibold">Where are the error traces?</h3>
          <p className="text-xs text-muted">
            {formatNumber(byDimension.error_traces)} failing trace
            {byDimension.error_traces === 1 ? "" : "s"} · {byDimension.reason}
          </p>
        </div>

        <div
          className="rounded-xl p-3"
          style={{
            borderLeft: "4px solid var(--err)",
            background: "var(--err-soft)",
            color: "var(--err-ink)",
          }}
        >
          <div className="text-base leading-snug">
            Most failures are at <span className="font-semibold">{top.value}</span> ({formatNumber(top.errors)}).
          </div>
          <div className="mt-2 text-sm">
            <Link
              to={`/integrations/${encodeURIComponent(integrationId)}/messages${ERRORS_ONLY_QUERY}`}
              className="font-medium underline-offset-2 hover:underline"
              style={{ color: "var(--err-ink)" }}
            >
              see all {formatNumber(byDimension.error_traces)} failed →
            </Link>
          </div>
        </div>

        <div className="space-y-1.5">
          {byDimension.buckets.map((b) => (
            <div key={b.value} className="flex items-center gap-3 text-sm">
              <span className="min-w-0 flex-1 truncate" title={b.value}>{b.value}</span>
              {/* Bars are scaled to the LARGEST bucket, not to a total.
                  A trace that failed at two operations is counted at
                  both, so the rows deliberately sum to more than the
                  trace count and a percentage would read as nonsense. */}
              <span
                aria-hidden
                style={{
                  height: 6,
                  width: `${(b.errors / max) * 120}px`,
                  background: "var(--err)",
                  borderRadius: 3,
                  flex: "none",
                }}
              />
              <span className="w-10 text-right font-mono text-xs text-muted">
                {formatNumber(b.errors)}
              </span>
            </div>
          ))}
        </div>

        <p className="text-xs text-muted">
          A trace that failed at more than one {label} is counted at each, so these add up to more
          than {formatNumber(byDimension.error_traces)}.
        </p>

        {showAlert && (
          <CreateTraceAlertDrawer
            integrationId={integrationId}
            onClose={() => setShowAlert(false)}
          />
        )}
      </div>
    );
  }


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
              <ServiceRef
                key={s.service_name}
                name={s.service_name}
                suffix="?tab=health"
                className="font-medium underline-offset-2 hover:underline"
              >
                {s.service_name} →
              </ServiceRef>
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
      // Every facet, not the first one. Taking `find(...)` here dated
      // back to a single ServiceType slot and quietly mislabelled any
      // multi-facet service — a queue consumer that also collects files
      // showed whichever the registry happened to declare first.
      facets: (s.service_facets ?? []).filter((f) => f.slug !== "core").map((f) => f.name),
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
            <ServiceRef
              name={top.service_name}
              className="font-semibold underline underline-offset-2 hover:no-underline"
            >
              {top.service_name}
            </ServiceRef>
          ) : (
            <span className="font-semibold">an unnamed service</span>
          )}
          {top.facets.length > 0 && (
            <span style={{ color: "color-mix(in oklab, var(--err-ink) 70%, transparent)" }}>
              {" "}
              · {top.facets.join(" · ")}
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
        {b.facets.length > 0 && <div className="truncate text-xs text-muted" title={b.facets.join(" · ")}>{b.facets.join(" · ")}</div>}
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
