// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// HealthCheckResultDrawer — the slide-in blade opened from a row on a
// service's Health checks tab. It shows the *actual result* behind the
// check (the live evidence), then a deep link into the full Logs / Metrics
// / Traces view with the relevant filter applied:
//
//   metric → previewAlertRule (current aggregated value vs threshold)
//   log    → listServiceLogs filtered by the check's severity/body/attrs
//   trace  → serviceTraces (failed traces, or slowest for a latency rule)
//   built-in "error span" → the service's unacknowledged error traces
//
// Pure read: every link is a callback into ServiceDetail so navigation +
// time-window widening stay owned by the page.

import { useEffect, useState } from "react";
import { EditDrawer } from "../primitives";
import { api } from "../../api/client";
import type { AlertRule, LogEntry, TraceSummary, AlertPreview, TraceVolumeRuleSpec } from "../../api/types";
import { alertCondition, logSevLabel, fmtWindow } from "../../lib/alertRule";
import { formatNumber, formatDurationMs, formatRelative } from "../../lib/format";

const OP_GLYPH: Record<string, string> = { gt: ">", gte: "≥", lt: "<", lte: "≤", eq: "=", neq: "≠" };

// The window over which the built-in error-span check's evidence is
// fetched — matches the backend openErrorLookback so the offending traces
// are in range even when the header window is narrow.
const OPEN_ERROR_WINDOW = "30d";

type Kind = "metric" | "log" | "trace_error" | "trace_latency" | "trace_volume" | "error_span";

function kindOf(rule: AlertRule | null, builtin: boolean): Kind {
  if (builtin) return "error_span";
  if (!rule) return "error_span";
  if (rule.signal === "metric") return "metric";
  if (rule.signal === "log") return "log";
  if (rule.trace_latency_spec) return "trace_latency";
  if (rule.trace_volume_spec) return "trace_volume";
  return "trace_error";
}

export default function HealthCheckResultDrawer({
  rule,
  builtin = false,
  serviceName,
  window: win,
  firing,
  openErrorCount = 0,
  onClose,
  canEdit = false,
  onEdit,
  onOpenLogs,
  onOpenMetrics,
  onOpenTraces,
}: {
  rule: AlertRule | null;
  builtin?: boolean;
  serviceName: string;
  window: string;
  firing: boolean;
  openErrorCount?: number;
  onClose: () => void;
  canEdit?: boolean;
  onEdit?: () => void;
  onOpenLogs: (minSeverity: number, body: string) => void;
  onOpenMetrics: (metric: string, attrs?: { key: string; op: string; value: string }[]) => void;
  onOpenTraces: (errorsOnly: boolean, widen: boolean) => void;
}) {
  const kind = kindOf(rule, builtin);
  const title = builtin ? "Error span detected" : (rule?.name ?? "Health check");

  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);
  const [metric, setMetric] = useState<AlertPreview | null>(null);
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [traces, setTraces] = useState<TraceSummary[]>([]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setErr(null);
    setMetric(null);
    setLogs([]);
    setTraces([]);

    const done = <T,>(set: (v: T) => void) => (v: T) => {
      if (!cancelled) {
        set(v);
        setLoading(false);
      }
    };
    const fail = (e: unknown) => {
      if (!cancelled) {
        setErr(String((e as Error)?.message ?? e));
        setLoading(false);
      }
    };

    if (kind === "metric" && rule && rule.spec?.metric_name) {
      api.previewAlertRule(rule.spec, rule.service_name || undefined).then(done(setMetric)).catch(fail);
    } else if (kind === "metric") {
      // A metric check with no metric_name can't be previewed (the backend
      // rejects it) — show it without a live value rather than 400ing.
      setLoading(false);
    } else if (kind === "log" && rule?.log_spec) {
      const s = rule.log_spec;
      api
        .listServiceLogs(serviceName, win, {
          minSeverity: s.min_severity || undefined,
          q: s.body_contains || undefined,
          limit: 25,
        })
        .then(done((r) => setLogs(r.logs ?? [])))
        .catch(fail);
    } else if (kind === "error_span") {
      api
        .serviceTraces(serviceName, OPEN_ERROR_WINDOW, { onlyFailed: true })
        .then(done((r) => setTraces(r.traces ?? [])))
        .catch(fail);
    } else if (kind === "trace_error") {
      api
        .serviceTraces(serviceName, win, { onlyFailed: true })
        .then(done((r) => setTraces(r.traces ?? [])))
        .catch(fail);
    } else if (kind === "trace_latency") {
      api
        .serviceTraces(serviceName, win)
        .then(done((r) => setTraces([...(r.traces ?? [])].sort((a, b) => b.duration_ms - a.duration_ms))))
        .catch(fail);
    } else if (kind === "trace_volume" && rule?.trace_volume_spec) {
      // Count over the rule's OWN window (not the page header window) so the
      // evidence matches the condition — fmtWindow yields an h/m/s string the
      // traces endpoint parses.
      api
        .serviceTraces(serviceName, fmtWindow(rule.trace_volume_spec.window_seconds))
        .then(done((r) => setTraces(r.traces ?? [])))
        .catch(fail);
    } else {
      setLoading(false);
    }
    return () => {
      cancelled = true;
    };
  }, [kind, rule, serviceName, win]);

  return (
    <EditDrawer title={title} width="medium" onClose={onClose}>
      <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
        {/* Header: signal + firing state + condition */}
        <div>
          <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
            <span className={`m-rule-badge ${firing ? "sev-critical" : ""}`}>{firing ? "Firing" : "Passing"}</span>
            <span className="m-rule-badge">{badgeFor(kind)}</span>
            {builtin && <span className="m-rule-badge">built-in</span>}
          </div>
          <div className="mono" style={{ fontSize: 12.5, color: "var(--muted)" }}>
            {builtin
              ? "Any unacknowledged error span on this service → unhealthy until acknowledged"
              : rule
                ? alertCondition(rule)
                : ""}
          </div>
        </div>

        {/* Evidence */}
        {loading ? (
          <div className="placeholder" style={{ margin: 0 }}>Loading…</div>
        ) : err ? (
          <div className="alert alert--error" style={{ margin: 0 }}>{err}</div>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
            {kind === "metric" && <MetricEvidence rule={rule!} preview={metric} />}
            {kind === "log" && <LogEvidence logs={logs} />}
            {kind === "trace_volume" && rule?.trace_volume_spec && (
              <VolumeEvidence traces={traces} spec={rule.trace_volume_spec} />
            )}
            {(kind === "trace_error" || kind === "trace_latency" || kind === "error_span") && (
              <TraceEvidence traces={traces} latency={kind === "trace_latency"} />
            )}
          </div>
        )}

        {/* Deep link into the full view, filter applied */}
        <div className="m-builder-actions">
          {kind === "metric" && rule && (
            <button className="btn btn--primary" type="button" onClick={() => onOpenMetrics(rule.spec.metric_name, rule.spec.attrs)}>
              Open in Metrics →
            </button>
          )}
          {kind === "log" && rule?.log_spec && (
            <button
              className="btn btn--primary"
              type="button"
              onClick={() => onOpenLogs(rule.log_spec!.min_severity, rule.log_spec!.body_contains)}
            >
              Open in Logs →
            </button>
          )}
          {kind === "trace_error" && (
            <button className="btn btn--primary" type="button" onClick={() => onOpenTraces(true, false)}>
              Open error traces →
            </button>
          )}
          {(kind === "trace_latency" || kind === "trace_volume") && (
            <button className="btn btn--primary" type="button" onClick={() => onOpenTraces(false, false)}>
              Open traces →
            </button>
          )}
          {kind === "error_span" && (
            <button className="btn btn--primary" type="button" onClick={() => onOpenTraces(true, true)}>
              Open error traces →
            </button>
          )}
          {canEdit && rule && !builtin && onEdit && (
            <button className="btn btn--primary" type="button" onClick={onEdit}>Edit health check</button>
          )}
          <button className="btn" type="button" onClick={onClose}>Close</button>
        </div>
        {kind === "error_span" && openErrorCount > 0 && (
          <p className="muted" style={{ fontSize: 12, margin: 0 }}>
            {formatNumber(openErrorCount)} unacknowledged error trace{openErrorCount === 1 ? "" : "s"} keep{openErrorCount === 1 ? "s" : ""} this service unhealthy.
          </p>
        )}
      </div>
    </EditDrawer>
  );
}

function badgeFor(kind: Kind): string {
  switch (kind) {
    case "metric":
      return "metric";
    case "log":
      return "log";
    default:
      return "trace";
  }
}

function MetricEvidence({ rule, preview }: { rule: AlertRule; preview: AlertPreview | null }) {
  if (!preview || !preview.has_data) {
    return <div className="muted" style={{ fontSize: 13 }}>No metric data in the current window.</div>;
  }
  const sp = rule.spec;
  const op = OP_GLYPH[sp.operator] ?? sp.operator;
  const unit = rule.unit ? ` ${rule.unit}` : "";
  return (
    <div className="svc-check" style={{ display: "block" }}>
      <div className="svc-check-name" style={{ marginBottom: 4 }}>{sp.aggregation} {sp.metric_name}</div>
      <div style={{ display: "flex", alignItems: "baseline", gap: 10 }}>
        <span className="mono" style={{ fontSize: 22, fontWeight: 600, color: preview.breached ? "var(--err)" : "var(--ink)" }}>
          {formatNumber(preview.value)}{unit}
        </span>
        <span className="muted mono" style={{ fontSize: 12.5 }}>
          threshold {op} {formatNumber(preview.threshold)}{unit} · {preview.samples} samples
        </span>
      </div>
      <div className="mono" style={{ fontSize: 12, marginTop: 4, color: preview.breached ? "var(--err)" : "var(--ok)" }}>
        {preview.breached ? "breaching now" : "within threshold now"}
      </div>
    </div>
  );
}

function LogEvidence({ logs }: { logs: LogEntry[] }) {
  if (logs.length === 0) {
    return <div className="muted" style={{ fontSize: 13 }}>No matching logs in the current window.</div>;
  }
  return (
    <>
      <div className="muted" style={{ fontSize: 12 }}>{logs.length} matching log{logs.length === 1 ? "" : "s"} (most recent)</div>
      <div style={{ display: "flex", flexDirection: "column", gap: 4, maxHeight: 320, overflow: "auto" }}>
        {logs.map((l, i) => (
          <div key={l.log_id ?? i} className="svc-check" style={{ display: "block", padding: "6px 8px" }}>
            <div style={{ display: "flex", gap: 8, alignItems: "center" }}>
              <span className="m-rule-badge">{logSevLabel(l.severity_number)}</span>
              <span className="muted mono" style={{ fontSize: 11 }}>{formatRelative(l.timestamp)}</span>
            </div>
            <div className="mono" style={{ fontSize: 12, marginTop: 2, whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{l.body}</div>
          </div>
        ))}
      </div>
    </>
  );
}

function VolumeEvidence({ traces, spec }: { traces: TraceSummary[]; spec: TraceVolumeRuleSpec }) {
  // serviceTraces caps its fetch, so a long list means "at least this many".
  const capped = traces.length >= 50;
  const countLabel = capped ? `${traces.length}+` : `${traces.length}`;
  const below = !capped && traces.length < spec.threshold;
  return (
    <div className="svc-check" style={{ display: "block" }}>
      <div className="svc-check-name" style={{ marginBottom: 4 }}>traces in the last {fmtWindow(spec.window_seconds)}</div>
      <div style={{ display: "flex", alignItems: "baseline", gap: 10 }}>
        <span className="mono" style={{ fontSize: 22, fontWeight: 600, color: below ? "var(--err)" : "var(--ink)" }}>
          {countLabel}
        </span>
        <span className="muted mono" style={{ fontSize: 12.5 }}>
          floor &lt; {formatNumber(spec.threshold)}
        </span>
      </div>
      <div className="mono" style={{ fontSize: 12, marginTop: 4, color: below ? "var(--err)" : "var(--ok)" }}>
        {below ? "below the floor now — traffic has dropped" : capped ? "well above the floor now" : "above the floor now"}
      </div>
      {traces.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 4, maxHeight: 240, overflow: "auto", marginTop: 8 }}>
          {traces.slice(0, 25).map((t) => (
            <div key={t.trace_id} style={{ display: "flex", gap: 8, alignItems: "center", justifyContent: "space-between" }}>
              <span className="mono" style={{ fontSize: 12 }}>{t.first_span_name || t.trace_id.slice(0, 12)}</span>
              <span className="muted mono" style={{ fontSize: 11 }}>{formatRelative(t.trace_start)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function TraceEvidence({ traces, latency }: { traces: TraceSummary[]; latency: boolean }) {
  if (traces.length === 0) {
    return <div className="muted" style={{ fontSize: 13 }}>No matching traces in the window.</div>;
  }
  return (
    <>
      <div className="muted" style={{ fontSize: 12 }}>
        {traces.length} {latency ? "trace" : "failed trace"}{traces.length === 1 ? "" : "s"} ({latency ? "slowest first" : "most recent"})
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 4, maxHeight: 320, overflow: "auto" }}>
        {traces.slice(0, 25).map((t) => (
          <div key={t.trace_id} className="svc-check" style={{ display: "block", padding: "6px 8px" }}>
            <div style={{ display: "flex", gap: 8, alignItems: "center", justifyContent: "space-between" }}>
              <span className="mono" style={{ fontSize: 12 }}>{t.first_span_name || t.trace_id.slice(0, 12)}</span>
              <span className="mono" style={{ fontSize: 12, color: t.has_error ? "var(--err)" : "var(--muted)" }}>{formatDurationMs(t.duration_ms)}</span>
            </div>
            <div className="muted mono" style={{ fontSize: 11, marginTop: 2 }}>
              {t.has_error ? "error · " : ""}{t.total_spans} spans · {formatRelative(t.trace_start)}
            </div>
          </div>
        ))}
      </div>
    </>
  );
}
