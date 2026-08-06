// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Health checks for a service or integration: a list of metric formulas
// that define its healthy state. Each is a metric (optionally scoped by
// attribute filters) aggregated over a window and compared to a
// threshold; any one firing flips the entity's health pill. Bound rules
// reuse the alert engine (alert_rules.service_name / integration_id).

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Link } from "react-router-dom";
import { api } from "../../api/client";
import type {
  AlertAggregation,
  AlertInstance,
  AlertOperator,
  AlertPreview,
  AlertRule,
  AlertSeverity,
  LogAttrFilter,
  LogFieldEntry,
  MetricRuleSpec,
} from "../../api/types";
import SearchableSelect from "../SearchableSelect";
import { EditDrawer } from "../primitives";
import { anchorTransform, useAnchoredPosition } from "../primitives/useAnchoredPosition";
import { CHECK_PARAM } from "../../lib/checkEditHref";
import { metricCheckHref as metricHref } from "../../lib/metricCheckHref";
import AttributeSuggest from "../logs/AttributeSuggest";
import FilterChip from "../logs/FilterChip";
import { AGG_LABELS, ALERT_AGGREGATIONS } from "../../lib/aggregations";
import { alertCondition, alertSignalLabel } from "../../lib/alertRule";
import { formatRelative } from "../../lib/format";

// CheckScope is what a health check governs. All three are first-class
// on the rule row (service_name / integration_id / system_id) and every
// evaluator resolves them the same way — a group scope becomes its member
// services before the query runs.
export type CheckScope = "service" | "integration" | "system";

// CheckKind is the rule type the editor builds. "metric" covers both the
// telemetry + pushed sources (toggled inside the metric editor).
type CheckKind = "metric" | "log" | "trace";

// scopeQuery / scopeBinding keep the "which field names this scope"
// decision in ONE place. It appears in the list query, the create/update
// payload and the preview; when it was spelled inline as a ternary,
// adding a third scope meant finding every one of them.
function scopeQuery(scope: CheckScope, target: string) {
  switch (scope) {
    case "service":
      return { service: target };
    case "integration":
      return { integration: target };
    case "system":
      return { system: target };
  }
}
const editorKindOf = (r: AlertRule): CheckKind =>
  r.signal === "log" ? "log" : r.signal === "trace" ? "trace" : "metric";

// Trailing-window choices for log/failed-trace checks, in seconds.
const WINDOW_CHOICES: { label: string; seconds: number }[] = [
  { label: "1m", seconds: 60 },
  { label: "5m", seconds: 300 },
  { label: "10m", seconds: 600 },
  { label: "30m", seconds: 1800 },
  { label: "1h", seconds: 3600 },
  { label: "6h", seconds: 21600 },
  { label: "12h", seconds: 43200 },
  { label: "1d", seconds: 86400 },
  { label: "2d", seconds: 172800 },
  { label: "7d", seconds: 604800 },
];
// OTLP SeverityNumber floors for the log-check severity picker.
const SEV_FLOORS: { label: string; value: number }[] = [
  { label: "Any", value: 0 },
  { label: "Info+", value: 9 },
  { label: "Warn+", value: 13 },
  { label: "Error+", value: 17 },
  { label: "Fatal", value: 21 },
];

const OPS: { op: AlertOperator; glyph: string }[] = [
  { op: "gt", glyph: ">" },
  { op: "gte", glyph: "≥" },
  { op: "lt", glyph: "<" },
  { op: "lte", glyph: "≤" },
  { op: "eq", glyph: "=" },
  { op: "neq", glyph: "≠" },
];
const WINDOWS = ["1m", "5m", "10m", "30m", "1h"];
const SEVERITIES: { v: AlertSeverity; label: string }[] = [
  { v: "info", label: "Info" },
  { v: "warning", label: "Warning" },
  { v: "critical", label: "Critical" },
];
const opGlyphOf = (op: string) => OPS.find((o) => o.op === op)?.glyph ?? op;

type ResolveMode = "auto" | "manual";

// ResolveModeField — shared "what happens when the condition clears" picker
// used by every check editor: auto-resolve (self-recovering) vs require
// acknowledgement (stays firing until a human clears it).
function ResolveModeField({ value, onChange }: { value: ResolveMode; onChange: (v: ResolveMode) => void }) {
  return (
    <div className="m-field">
      <label className="m-field-label">When the condition clears</label>
      <div className="m-seg">
        <button type="button" className={`m-seg-b ${value === "auto" ? "on" : ""}`} onClick={() => onChange("auto")}>
          Auto-resolve
        </button>
        <button type="button" className={`m-seg-b ${value === "manual" ? "on" : ""}`} onClick={() => onChange("manual")}>
          Require acknowledgement
        </button>
      </div>
      <p className="muted" style={{ fontSize: 12, margin: "4px 0 0" }}>
        {value === "auto"
          ? "The check recovers on its own once the metric/condition returns to normal."
          : "The check stays unhealthy until someone acknowledges it — even after the condition clears."}
      </p>
    </div>
  );
}

/**
 * The class for a check row's coloured spine.
 *
 * severity answers "how bad would this be if it fired"; it is a property
 * of the CONFIGURATION. Painting the row with it made every check look
 * permanently angry — a critical check that has never fired is not an
 * incident, it is a healthy check with a serious threshold.
 *
 * So severity only colours a row that is actually firing. Otherwise the
 * row shows its real state: healthy, or idle when nothing is being
 * asserted (disabled).
 */
export function barStateClass(rule: { severity: string; enabled: boolean }, isFiring: boolean): string {
  if (!rule.enabled) return "state-idle";
  if (!isFiring) return "state-ok";
  return `sev-${rule.severity}`;
}

export default function HealthChecks({
  scope,
  target,
  window: win,
  reloadKey,
  onChanged,
}: {
  scope: CheckScope;
  target: string;
  window: string;
  // Bump to force a re-fetch when checks change outside this component
  // (e.g. a monitoring template is applied on the parent edit screen).
  reloadKey?: number;
  // Called after any change that can move the parent's health: adding,
  // editing, deleting or toggling a check. Without it the page keeps
  // showing the status it loaded with, and only a browser refresh made
  // the new state appear.
  onChanged?: () => void;
}) {
  const [rules, setRules] = useState<AlertRule[]>([]);
  // Opened once, when arriving from a ?check= deep link. A ref, not
  // state: re-opening the drawer every time the list refreshes would
  // make it impossible to close.
  const deepLinkHandled = useRef(false);
  // Which checks are firing RIGHT NOW. The rules list carries a check's
  // configured severity, which is what it would be worth if it fired —
  // not whether it has. Colouring by severity alone painted every row
  // amber or red permanently.
  // Keyed by rule id, holding the INSTANCE — the start time is what
  // answers "how long has this been wrong", which the list could not say
  // at all: it showed that a check existed, not that it had been firing
  // since Tuesday.
  const [firing, setFiring] = useState<Map<string, AlertInstance>>(new Map());
  const [loading, setLoading] = useState(true);
  // editing = an existing rule (PUT); creating = a new rule of a kind (POST).
  const [editing, setEditing] = useState<AlertRule | null>(null);
  const [creating, setCreating] = useState<CheckKind | null>(null);
  const [addOpen, setAddOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(() => {
    setLoading(true);
    api
      .listAlertRules(scopeQuery(scope, target))
      .then((r) => setRules(r.rules ?? []))
      .catch(() => setRules([]))
      .finally(() => setLoading(false));
    // Firing state is a separate feed and deliberately not awaited: a
    // slow or failed instances call must not stop the checks rendering.
    // An empty set just means "nothing known to be firing", which is the
    // honest reading of no data.
    api
      .listAlertInstances()
      .then((r) =>
        setFiring(
          new Map(
            (r.instances ?? [])
              .filter((i) => i.state === "firing")
              .map((i) => [i.alert_rule_id, i] as const),
          ),
        ),
      )
      .catch(() => setFiring(new Map()));
  }, [scope, target]);

  useEffect(() => {
    refresh();
  }, [refresh, reloadKey]);

  // Open the drawer for a check linked to from an Errors page. Landing on
  // the page is not the same as being able to act — a scope can carry a
  // dozen checks, and finding the one you clicked was left to the reader.
  //
  // The parameter is dropped once consumed, so a refresh does not reopen
  // a drawer the user has closed, and the URL stays shareable as "this
  // page" rather than "this page, editing something".
  useEffect(() => {
    if (deepLinkHandled.current || rules.length === 0) return;
    const params = new URLSearchParams(window.location.search);
    const wanted = params.get(CHECK_PARAM);
    if (!wanted) return;
    deepLinkHandled.current = true;
    const rule = rules.find((r) => r.id === wanted);
    if (rule) {
      setCreating(null);
      setEditing(rule);
    }
    params.delete(CHECK_PARAM);
    const q = params.toString();
    window.history.replaceState(null, "", `${window.location.pathname}${q ? `?${q}` : ""}`);
  }, [rules]);

  const remove = async (id: string) => {
    if (!window.confirm("Delete this health check?")) return;
    try {
      await api.deleteAlertRule(id);
      refresh();
      onChanged?.();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  // The add-check menu is portaled to <body>: `.card` sets overflow
  // hidden for its rounded corners, which clipped the menu at the card's
  // edge and hid its last option entirely.
  const addBtnRef = useRef<HTMLButtonElement | null>(null);
  const addMenuRef = useRef<HTMLDivElement | null>(null);
  const addCoords = useAnchoredPosition(addBtnRef, addOpen, { align: "right", minWidth: 200 });

  useEffect(() => {
    if (!addOpen) return;
    const onPointer = (e: MouseEvent) => {
      const t = e.target as Node;
      // Portaled, so it is not inside the button's subtree — test both.
      if (addBtnRef.current?.contains(t) || addMenuRef.current?.contains(t)) return;
      setAddOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setAddOpen(false);
    };
    document.addEventListener("mousedown", onPointer);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onPointer);
      document.removeEventListener("keydown", onKey);
    };
  }, [addOpen]);

  const drawerOpen = editing != null || creating != null;
  const kind: CheckKind = editing ? editorKindOf(editing) : creating ?? "metric";
  const closeDrawer = () => {
    setEditing(null);
    setCreating(null);
    setAddOpen(false);
  };
  const onSaved = () => {
    closeDrawer();
    refresh();
    onChanged?.();
  };

  // Pushed-source checks only make sense on a service, but every check
  // KIND is available on both scopes.
  const addTypes: { kind: CheckKind; label: string }[] = [
    { kind: "metric", label: "Metric / pushed value" },
    { kind: "log", label: "Log match" },
    { kind: "trace", label: "Failed traces, response time, or low traffic" },
  ];

  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="card__header" style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
        <span>Health checks{rules.length > 0 ? ` · ${rules.length}` : ""}</span>
        {!drawerOpen && (
          <div style={{ position: "relative" }}>
            <button
              ref={addBtnRef}
              className="btn btn--sm"
              type="button"
              onClick={() => setAddOpen((o) => !o)}
              aria-expanded={addOpen}
              aria-haspopup="menu"
            >
              + Add health check
            </button>
            {addOpen && addCoords && createPortal(
              <div
                ref={addMenuRef}
                role="menu"
                style={{
                  position: "fixed",
                  top: addCoords.top,
                  left: addCoords.left,
                  transform: anchorTransform(addCoords, "right"),
                  // Above .edit-drawer-root (z 2000), matching SearchableSelect,
                  // so the menu is usable from inside an edit drawer too.
                  zIndex: 2100,
                  maxHeight: addCoords.maxHeight,
                  overflowY: "auto",
                  background: "var(--surface-2)", border: "1px solid var(--border)",
                  borderRadius: 8, padding: 4, minWidth: addCoords.minWidth,
                  boxShadow: "0 6px 24px rgba(0,0,0,0.18)",
                  display: "flex", flexDirection: "column", gap: 2,
                }}
              >
                {addTypes.map((t) => (
                  <button
                    key={t.kind}
                    type="button"
                    role="menuitem"
                    className="btn btn--sm"
                    style={{ justifyContent: "flex-start", background: "transparent", border: "none", textAlign: "left" }}
                    onClick={() => { setCreating(t.kind); setEditing(null); setAddOpen(false); }}
                  >
                    {t.label}
                  </button>
                ))}
              </div>,
              document.body,
            )}
          </div>
        )}
      </div>
      <div style={{ padding: "12px 16px", display: "flex", flexDirection: "column", gap: 10 }}>
        <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
          The checks that define this {scope}'s healthy state — a metric formula, a log match, failed traces, or
          response time. Any one firing flips the health pill. Add, edit, or remove them here.
        </p>

        {error && <div className="alert alert--error" style={{ margin: 0 }}>{error}</div>}

        {loading ? (
          <div className="placeholder" style={{ margin: 0 }}>Loading…</div>
        ) : (
          rules.length > 0 && (
            <div className="m-existing">
              {rules.map((rule) => (
                <div key={rule.id} className="m-existing-row">
                  <div className={`m-ex-bar ${barStateClass(rule, firing.has(rule.id))}`} />
                  <div className="m-ex-mid">
                    <div className="m-ex-name">
                      {rule.name}
                      {alertSignalLabel(rule.signal) && (
                        <span className="m-rule-badge" style={{ marginLeft: 6 }}>{alertSignalLabel(rule.signal)}</span>
                      )}
                      {rule.source === "pushed" && <span className="m-rule-badge" style={{ marginLeft: 6 }}>pushed</span>}
                      {rule.display_on_service && <span className="badge-brand" style={{ marginLeft: 6 }}>on page</span>}
                      {rule.resolve_mode === "manual" && <span className="m-rule-badge" style={{ marginLeft: 6 }}>needs ack</span>}
                    </div>
                    {/* Reads per signal — metric / pushed / log / failed-trace. */}
                    <div className="m-ex-cond">{alertCondition(rule)}</div>
                    {/* How long it has been wrong. A list that says only
                        "unhealthy" leaves you unable to tell a blip that
                        started a minute ago from something broken since
                        last week — and that is usually the first thing
                        worth knowing. */}
                    {firing.get(rule.id) && (
                      <div className="m-ex-cond" style={{ color: "var(--err)" }}>
                        firing for {formatRelative(firing.get(rule.id)!.started_at).replace(/\s*ago$/, "")}
                        {" · since "}
                        {new Date(firing.get(rule.id)!.started_at).toLocaleString()}
                      </div>
                    )}
                  </div>
                  <span className={`m-rule-badge sev-${rule.severity}`}>{rule.severity}</span>
                  {/* The telemetry behind the check, pre-filtered to the
                      metric and the rule's own attribute predicates, and
                      widened to cover the firing so the onset is on
                      screen rather than off the left edge. Only for
                      metric checks — the others have no single series to
                      open. */}
                  {metricHref(rule, firing.get(rule.id)) && (
                    <Link className="m-ex-tgt" to={metricHref(rule, firing.get(rule.id))!} title="Open this metric, filtered as the check sees it">
                      view metric →
                    </Link>
                  )}
                  <button className="m-ex-tgt" type="button" onClick={() => { setCreating(null); setEditing(rule); }}>edit</button>
                  <button className="m-ex-tgt" type="button" onClick={() => remove(rule.id)}>remove</button>
                </div>
              ))}
            </div>
          )
        )}

        {drawerOpen && (
          <EditDrawer
            // Remount per rule/kind so the editor's state initialises fresh.
            key={editing ? editing.id : `new-${creating}`}
            title={
              editing
                ? `Edit “${editing.name}”`
                : kind === "log"
                  ? "New log check"
                  : kind === "trace"
                    ? "New trace check"
                    : "New metric check"
            }
            width="wide"
            onClose={closeDrawer}
          >
            {kind === "metric" && (
              <HealthCheckEditor scope={scope} target={target} window={win} rule={editing} onSaved={onSaved} onCancel={closeDrawer} />
            )}
            {kind === "log" && (
              <LogCheckEditor scope={scope} target={target} rule={editing} onSaved={onSaved} onCancel={closeDrawer} />
            )}
            {kind === "trace" && (
              <TraceCheckEditor scope={scope} target={target} rule={editing} onSaved={onSaved} onCancel={closeDrawer} />
            )}
          </EditDrawer>
        )}
      </div>
    </div>
  );
}

// HealthCheckEditDrawer is the standalone edit blade for one existing health
// check — the same EditDrawer + kind-specific editor the checks list uses,
// but openable on its own (e.g. from the health-check result drawer on a
// service page) without entering the full service-edit screen.
export function HealthCheckEditDrawer({
  rule,
  scope,
  target,
  window: win,
  onSaved,
  onClose,
}: {
  rule: AlertRule;
  scope: CheckScope;
  target: string;
  window: string;
  onSaved: () => void;
  onClose: () => void;
}) {
  const kind = editorKindOf(rule);
  return (
    <EditDrawer key={rule.id} title={`Edit “${rule.name}”`} width="wide" onClose={onClose}>
      {kind === "metric" && <HealthCheckEditor scope={scope} target={target} window={win} rule={rule} onSaved={onSaved} onCancel={onClose} />}
      {kind === "log" && <LogCheckEditor scope={scope} target={target} rule={rule} onSaved={onSaved} onCancel={onClose} />}
      {kind === "trace" && <TraceCheckEditor scope={scope} target={target} rule={rule} onSaved={onSaved} onCancel={onClose} />}
    </EditDrawer>
  );
}

function HealthCheckEditor({
  scope,
  target,
  window: win,
  rule,
  onSaved,
  onCancel,
}: {
  scope: CheckScope;
  target: string;
  window: string;
  // When set, the editor edits this existing rule (PUT); otherwise it
  // creates a new one (POST). The parent remounts the editor per target
  // (keyed), so these initialisers run against the right rule.
  rule: AlertRule | null;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const editingExisting = !!rule;
  // Pushed-source checks only make sense on a service (there must be a
  // service to feed values to + display the tile on).
  const canPush = scope === "service";
  const [source, setSource] = useState<"telemetry" | "pushed">(rule?.source === "pushed" ? "pushed" : "telemetry");
  const pushed = canPush && source === "pushed";
  const [name, setName] = useState(rule?.name ?? "");
  const [runbook, setRunbook] = useState(rule?.runbook ?? "");
  const [metricNames, setMetricNames] = useState<string[]>([]);
  const [metric, setMetric] = useState(rule?.spec.metric_name ?? "");
  const [fields, setFields] = useState<LogFieldEntry[]>([]);
  const [attrs, setAttrs] = useState<LogAttrFilter[]>(
    (rule?.spec.attrs ?? []).map((a) => ({ key: a.key, op: a.op, value: a.value })) as LogAttrFilter[],
  );
  const [attrOpen, setAttrOpen] = useState(false);
  const [agg, setAgg] = useState<AlertAggregation>(rule?.spec.aggregation ?? "max");
  const [op, setOp] = useState<AlertOperator>(rule?.spec.operator ?? "gt");
  const [threshold, setThreshold] = useState(rule?.spec.threshold ?? 100);
  const [forWindow, setForWindow] = useState(rule?.spec.for_window || "5m");
  // "Also alert if the metric stops reporting" — off unless the saved
  // rule asked for it, since silence is normal for most metrics.
  const [fireOnNoData, setFireOnNoData] = useState(rule?.spec.fire_on_no_data ?? false);
  const [severity, setSeverity] = useState<AlertSeverity>(rule?.severity ?? "critical");
  // "Show on service page": surface the latest reading as a value tile.
  const [displayOnService, setDisplayOnService] = useState(rule?.display_on_service ?? false);
  const [unit, setUnit] = useState(rule?.unit ?? "");
  // Metric checks default to auto-resolve (self-recovering).
  const [resolveMode, setResolveMode] = useState<ResolveMode>(rule?.resolve_mode === "manual" ? "manual" : "auto");
  const [preview, setPreview] = useState<AlertPreview | null>(null);
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  useEffect(() => {
    api.metricCatalog(win, {}).then((r) => setMetricNames((r.metrics ?? []).map((m) => m.name))).catch(() => setMetricNames([]));
  }, [win]);

  // Load the chosen metric's attribute keys for the filter picker. Skip the
  // attrs-reset on the first run so a rule's pre-filled attributes survive
  // mount; clear them only when the user actually switches metric.
  const firstFieldsRun = useRef(true);
  useEffect(() => {
    if (firstFieldsRun.current) {
      firstFieldsRun.current = false;
    } else {
      setAttrs([]);
    }
    if (!metric) {
      setFields([]);
      return;
    }
    api.metricFields(win, metric).then((r) => setFields(r.fields ?? [])).catch(() => setFields([]));
  }, [metric, win]);

  const spec: MetricRuleSpec = useMemo(
    () => ({
      metric_name: metric,
      aggregation: agg,
      operator: op,
      threshold,
      for_window: forWindow,
      attrs: attrs.map((a) => ({ key: a.key, op: a.op, value: a.value })),
      fire_on_no_data: fireOnNoData,
    }),
    [metric, agg, op, threshold, forWindow, attrs, fireOnNoData],
  );

  const debounce = useRef<number | undefined>(undefined);
  useEffect(() => {
    // Pushed checks have no metric to aggregate, so there's nothing to
    // preview against ClickHouse.
    if (!metric || pushed) {
      setPreview(null);
      return;
    }
    window.clearTimeout(debounce.current);
    debounce.current = window.setTimeout(() => {
      // Scope the preview to this service so it matches how the check actually
      // evaluates (service-bound rules only aggregate their own service).
      api
        .previewAlertRule(
          spec,
          scope === "service" ? target : undefined,
          scope === "integration" ? target : undefined,
          scope === "system" ? target : undefined,
        )
        .then(setPreview)
        .catch(() => setPreview(null));
    }, 350);
    return () => window.clearTimeout(debounce.current);
  }, [spec, metric, pushed, scope, target]);

  const recent = useMemo(() => fields.slice(0, 4).map((f) => f.key), [fields]);
  const addFilter = (f: LogAttrFilter) => {
    setAttrs((cur) => (cur.some((c) => c.key === f.key && c.op === f.op) ? cur : [...cur, f]));
    setAttrOpen(false);
  };

  const save = async () => {
    if (!pushed && !metric) {
      setErr("Pick a metric first.");
      return;
    }
    const finalName =
      name.trim() || (pushed ? "Pushed value" : metric ? `${metric} health` : "Health check");
    // A pushed check carries no metric binding — only the operator +
    // threshold define a breach (the value is fed in externally).
    const finalSpec: MetricRuleSpec = pushed
      ? { metric_name: "", aggregation: "last", operator: op, threshold, for_window: "5m", attrs: [] }
      : spec;
    setSaving(true);
    setErr(null);
    // Common fields for both create and update. A health check is always a
    // metric-signal rule (telemetry or pushed source).
    const body = {
      name: finalName,
      runbook: runbook.trim(),
      severity,
      signal: "metric" as const,
      spec: finalSpec,
      source: (pushed ? "pushed" : "telemetry") as "telemetry" | "pushed",
      display_on_service: scope === "service" ? displayOnService : false,
      unit: unit.trim() || undefined,
      resolve_mode: resolveMode,
      ...scopeBinding(scope, target),
    };
    try {
      if (rule) {
        // Full PUT — preserve the rule's enable state, routed channels,
        // owning team and any custom templates so an edit doesn't silently
        // drop them.
        await api.updateAlertRule(rule.id, {
          ...body,
          description: rule.description,
          enabled: rule.enabled,
          channel_ids: rule.channel_ids,
          group_id: rule.group_id,
          title_template: rule.title_template,
          body_template: rule.body_template,
        });
      } else {
        await api.createAlertRule({ ...body, enabled: true, channel_ids: [] });
      }
      onSaved();
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    // Rendered inside an EditDrawer body — the drawer panel already
    // provides padding + surface, so this is just a vertical stack of
    // .m-field rows. The original .m-builder-card class added a
    // bordered tile that would have nested visually inside the drawer.
    <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
      {canPush && (
        <div className="m-field">
          <label className="m-field-label">Value source</label>
          <div className="m-seg">
            <button type="button" className={`m-seg-b ${source === "telemetry" ? "on" : ""}`} onClick={() => setSource("telemetry")}>
              From telemetry
            </button>
            <button type="button" className={`m-seg-b ${source === "pushed" ? "on" : ""}`} onClick={() => setSource("pushed")}>
              Pushed externally
            </button>
          </div>
          <p className="muted" style={{ fontSize: 12, margin: "4px 0 0" }}>
            {pushed
              ? "A scraper POSTs the current value to Sluicio; the check compares it to the threshold."
              : "Sluicio computes the value by aggregating an OTLP metric over a window."}
          </p>
        </div>
      )}

      <div className="m-field">
        <label className="m-field-label">Name</label>
        <input
          className="search__input"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder={pushed ? "e.g. Queue depth" : metric ? `${metric} health` : "Health check name"}
        />
      </div>

      <div className="m-field">
        <label className="m-field-label">Runbook <span className="muted" style={{ fontWeight: 400 }}>optional</span></label>
        <textarea
          className="search__input"
          style={{ minHeight: 68, resize: "vertical", fontFamily: "inherit" }}
          value={runbook}
          onChange={(e) => setRunbook(e.target.value)}
          placeholder="What to do when this fires — e.g. Check the consumer group first; the usual cause is a stuck partition. Escalate to #payments-oncall after 15 minutes."
        />
        <div className="muted" style={{ fontSize: 11.5, marginTop: 4 }}>
          Sent with every notification and available to agents over MCP, so whoever picks this
          up gets your playbook instead of guessing.
        </div>
      </div>

      {!pushed && (
        <div className="m-field">
          <label className="m-field-label">Metric</label>
          <SearchableSelect
            value={metric}
            onChange={setMetric}
            options={metricNames}
            allLabel="Choose a metric…"
            placeholder="Filter metrics…"
          />
        </div>
      )}

      {!pushed && (
      <div className="m-field">
        <label className="m-field-label">Attributes (optional)</label>
        <div style={{ display: "flex", gap: 6, flexWrap: "wrap", alignItems: "center" }}>
          {attrs.map((a, i) => (
            <FilterChip key={`${a.key}-${i}`} k={a.key} op={a.op} value={a.value} onRemove={() => setAttrs((cur) => cur.filter((_, j) => j !== i))} />
          ))}
          <div style={{ position: "relative" }}>
            <button type="button" className="addfilter" disabled={!metric} aria-expanded={attrOpen} onClick={() => setAttrOpen((o) => !o)}>
              + filter
            </button>
            {attrOpen && (
              <AttributeSuggest
                fields={fields}
                recent={recent}
                window={win}
                onPick={addFilter}
                onClose={() => setAttrOpen(false)}
                fetchValues={(key, w) => api.metricAttributeValues(key, w, 50, metric)}
                keyPlaceholder="Filter attributes by name…"
                footHint="attributes scope the formula"
              />
            )}
          </div>
        </div>
      </div>
      )}

      {pushed ? (
        <div className="m-rule-sentence">
          <span className="m-rs-prose">When the pushed value is</span>
          <select className="m-rs-sel" value={op} onChange={(e) => setOp(e.target.value as AlertOperator)}>
            {OPS.map((o) => <option key={o.op} value={o.op}>{o.glyph}</option>)}
          </select>
          <input className="m-rs-num" type="number" value={threshold} onChange={(e) => setThreshold(Number(e.target.value))} />
          {unit && <span className="m-rs-prose">{unit}</span>}
        </div>
      ) : (
        <div className="m-rule-sentence">
          <span className="m-rs-prose">When</span>
          <select className="m-rs-sel" value={agg} onChange={(e) => setAgg(e.target.value as AlertAggregation)}>
            {ALERT_AGGREGATIONS.map((a) => <option key={a} value={a}>{AGG_LABELS[a]}</option>)}
          </select>
          <span className="m-rs-prose">of</span>
          <span className="m-rs-metric mono">{metric || "metric"}</span>
          <span className="m-rs-prose">is</span>
          <select className="m-rs-sel" value={op} onChange={(e) => setOp(e.target.value as AlertOperator)}>
            {OPS.map((o) => <option key={o.op} value={o.op}>{o.glyph}</option>)}
          </select>
          <input className="m-rs-num" type="number" value={threshold} onChange={(e) => setThreshold(Number(e.target.value))} />
          <span className="m-rs-prose">for</span>
          <select className="m-rs-sel" value={forWindow} onChange={(e) => setForWindow(e.target.value)}>
            {WINDOWS.map((wn) => <option key={wn} value={wn}>{wn}</option>)}
          </select>
        </div>
      )}

      {!pushed && metric && (
        <div className="m-field">
          <label className="m-field-label">No data</label>
          <label style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 13, cursor: "pointer" }}>
            <input
              type="checkbox"
              checked={fireOnNoData}
              onChange={(e) => setFireOnNoData(e.target.checked)}
            />
            Also alert if this metric stops reporting
          </label>
          <p className="muted" style={{ fontSize: 12, margin: "4px 0 0" }}>
            {fireOnNoData
              ? "A window with no data at all counts as unhealthy — for probes and heartbeats, where silence means nobody is checking any more. Starts watching once the check has seen data at least once, so saving this before the exporter exists won't alert."
              : "Silence is treated as \u201cnothing to report\u201d: the check only fires on a measured value that breaches the threshold."}
          </p>
        </div>
      )}

      {!pushed && metric && preview && (
        !preview.has_data ? (
          <div className={`m-preview ${fireOnNoData ? "breach" : "ok"}`}>
            <span className="m-preview-pip" />
            <span>
              No data for this metric/scope in the window.
              {fireOnNoData ? " Once this check has seen data, that would make it unhealthy." : ""}
            </span>
          </div>
        ) : preview.breached ? (
          <div className="m-preview breach">
            <span className="m-preview-pip" />
            <span><b>Would be unhealthy now.</b> Current <span className="mono">{preview.value.toLocaleString(undefined, { maximumFractionDigits: 2 })}</span> {opGlyphOf(op)} {preview.threshold}.</span>
          </div>
        ) : (
          <div className="m-preview ok">
            <span className="m-preview-pip" />
            <span><b>Healthy.</b> Current <span className="mono">{preview.value.toLocaleString(undefined, { maximumFractionDigits: 2 })}</span> is within the threshold.</span>
          </div>
        )
      )}

      <div className="m-field">
        <label className="m-field-label">Severity</label>
        <div className="m-seg">
          {SEVERITIES.map((s) => (
            <button key={s.v} type="button" className={`m-seg-b sev-${s.v} ${severity === s.v ? "on" : ""}`} onClick={() => setSeverity(s.v)}>
              {s.label}
            </button>
          ))}
        </div>
      </div>

      <ResolveModeField value={resolveMode} onChange={setResolveMode} />

      {scope === "service" && (
        <div className="m-field">
          <label className="m-field-label">Display</label>
          <label style={{ display: "flex", gap: 8, alignItems: "center", fontSize: 13, cursor: "pointer" }}>
            <input type="checkbox" checked={displayOnService} onChange={(e) => setDisplayOnService(e.target.checked)} />
            Show this check's latest value as a tile on the service page
          </label>
          {displayOnService && (
            <input
              className="input"
              style={{ marginTop: 8, maxWidth: 200 }}
              value={unit}
              onChange={(e) => setUnit(e.target.value)}
              placeholder="Unit (optional) — e.g. msgs, ms"
            />
          )}
        </div>
      )}

      {err && <div className="alert alert--error" style={{ margin: 0 }}>{err}</div>}

      <div className="m-builder-actions">
        <button className="btn btn--primary" type="button" disabled={saving || (!pushed && !metric)} onClick={save}>
          {saving ? "Saving…" : editingExisting ? "Save changes" : "Add health check"}
        </button>
        <button className="btn" type="button" onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}

// scopeBinding returns the service_name / integration_id / system_id
// field that binds a rule to its target, shared by every editor.
function scopeBinding(scope: CheckScope, target: string) {
  switch (scope) {
    case "service":
      return { service_name: target };
    case "integration":
      return { integration_id: target };
    case "system":
      return { system_id: target };
  }
}

// LogCheckEditor — create/edit a log-signal health check: fire when ≥N logs
// matching a severity floor + body substring arrive within the window.
function LogCheckEditor({
  scope,
  target,
  rule,
  onSaved,
  onCancel,
}: {
  scope: CheckScope;
  target: string;
  rule: AlertRule | null;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const ls = rule?.log_spec;
  const [name, setName] = useState(rule?.name ?? "");
  const [runbook, setRunbook] = useState(rule?.runbook ?? "");
  const [severity, setSeverity] = useState<AlertSeverity>(rule?.severity ?? "warning");
  const [minSeverity, setMinSeverity] = useState<number>(ls?.min_severity ?? 17);
  const [bodyContains, setBodyContains] = useState(ls?.body_contains ?? "");
  const [threshold, setThreshold] = useState<number>(ls?.threshold ?? 1);
  const [windowSec, setWindowSec] = useState<number>(ls?.window_seconds ?? 300);
  // Direction: "at_least" (default, a flood) or "fewer_than" (a drought —
  // e.g. "fewer than 1 heartbeat log in the last hour", where zero fires).
  const [comparison, setComparison] = useState<"at_least" | "fewer_than">(ls?.comparison === "fewer_than" ? "fewer_than" : "at_least");
  const below = comparison === "fewer_than";
  // Log checks default to require-acknowledgement (an error you didn't see
  // still happened).
  const [resolveMode, setResolveMode] = useState<ResolveMode>(rule?.resolve_mode === "auto" ? "auto" : "manual");
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const save = async () => {
    setSaving(true);
    setErr(null);
    const finalName = name.trim() || (bodyContains.trim() ? `Log: ${bodyContains.trim()}` : "Log check");
    const body = {
      name: finalName,
      runbook: runbook.trim(),
      severity,
      resolve_mode: resolveMode,
      signal: "log" as const,
      log_spec: {
        min_severity: minSeverity,
        body_contains: bodyContains.trim(),
        attrs: [],
        threshold: Math.max(1, Math.floor(threshold || 1)),
        window_seconds: windowSec,
        comparison,
      },
      ...scopeBinding(scope, target),
    };
    try {
      if (rule) {
        await api.updateAlertRule(rule.id, {
          ...body, description: rule.description, enabled: rule.enabled,
          channel_ids: rule.channel_ids, group_id: rule.group_id,
          title_template: rule.title_template, body_template: rule.body_template,
        });
      } else {
        await api.createAlertRule({ ...body, enabled: true, channel_ids: [] });
      }
      onSaved();
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
      <div className="m-field">
        <label className="m-field-label">Name</label>
        <input className="search__input" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Error logs" />
      </div>

      <div className="m-field">
        <label className="m-field-label">Runbook <span className="muted" style={{ fontWeight: 400 }}>optional</span></label>
        <textarea
          className="search__input"
          style={{ minHeight: 68, resize: "vertical", fontFamily: "inherit" }}
          value={runbook}
          onChange={(e) => setRunbook(e.target.value)}
          placeholder="What to do when this fires — e.g. Check the consumer group first; the usual cause is a stuck partition. Escalate to #payments-oncall after 15 minutes."
        />
        <div className="muted" style={{ fontSize: 11.5, marginTop: 4 }}>
          Sent with every notification and available to agents over MCP, so whoever picks this
          up gets your playbook instead of guessing.
        </div>
      </div>

      <div className="m-field">
        <label className="m-field-label">Match logs</label>
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "center" }}>
          <select className="m-rs-sel" value={minSeverity} onChange={(e) => setMinSeverity(Number(e.target.value))}>
            {SEV_FLOORS.map((s) => <option key={s.value} value={s.value}>{s.label}</option>)}
          </select>
          <input
            className="search__input"
            style={{ flex: 1, minWidth: 180 }}
            value={bodyContains}
            onChange={(e) => setBodyContains(e.target.value)}
            placeholder="body contains… (optional)"
          />
        </div>
      </div>

      <div className="m-rule-sentence">
        <span className="m-rs-prose">When</span>
        <select className="m-rs-sel" value={comparison} onChange={(e) => setComparison(e.target.value as "at_least" | "fewer_than")}>
          <option value="at_least">at least</option>
          <option value="fewer_than">fewer than</option>
        </select>
        <input className="m-rs-num" type="number" min={1} value={threshold} onChange={(e) => setThreshold(Number(e.target.value))} />
        <span className="m-rs-prose">matching log{threshold === 1 ? "" : "s"} in</span>
        <select className="m-rs-sel" value={windowSec} onChange={(e) => setWindowSec(Number(e.target.value))}>
          {WINDOW_CHOICES.map((w) => <option key={w.seconds} value={w.seconds}>{w.label}</option>)}
        </select>
      </div>
      {below && (
        <p className="muted" style={{ fontSize: 12, margin: "-8px 0 0" }}>
          Fires when matching logs drop below the floor — zero (a fully silent
          source) counts as below, so this doubles as a dead-man's-switch.
        </p>
      )}

      <div className="m-field">
        <label className="m-field-label">Severity</label>
        <div className="m-seg">
          {SEVERITIES.map((s) => (
            <button key={s.v} type="button" className={`m-seg-b sev-${s.v} ${severity === s.v ? "on" : ""}`} onClick={() => setSeverity(s.v)}>
              {s.label}
            </button>
          ))}
        </div>
      </div>

      <ResolveModeField value={resolveMode} onChange={setResolveMode} />

      {err && <div className="alert alert--error" style={{ margin: 0 }}>{err}</div>}

      <div className="m-builder-actions">
        <button className="btn btn--primary" type="button" disabled={saving} onClick={save}>
          {saving ? "Saving…" : rule ? "Save changes" : "Add health check"}
        </button>
        <button className="btn" type="button" onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}

// TraceCheckEditor — create/edit a trace health check. Three flavours share
// signal='trace': "Failed traces" (fire on ≥N error-span traces over the
// window), "Response time" (fire when windowed p95/max latency reaches a
// threshold), and "Low traffic" (fire when FEWER than N traces arrive — a
// dead-man's-switch where zero counts as below). A mode toggle picks the spec.
function TraceCheckEditor({
  scope,
  target,
  rule,
  onSaved,
  onCancel,
}: {
  scope: CheckScope;
  target: string;
  rule: AlertRule | null;
  onSaved: () => void;
  onCancel: () => void;
}) {
  const ts = rule?.trace_error_spec;
  const ls = rule?.trace_latency_spec;
  const vs = rule?.trace_volume_spec;
  // Existing latency/volume rule → that flavour; otherwise failed-trace.
  type TraceMode = "errors" | "latency" | "volume";
  const [mode, setMode] = useState<TraceMode>(ls ? "latency" : vs ? "volume" : "errors");
  const [name, setName] = useState(rule?.name ?? "");
  const [runbook, setRunbook] = useState(rule?.runbook ?? "");
  const [severity, setSeverity] = useState<AlertSeverity>(rule?.severity ?? "warning");
  // Failed-trace fields.
  const [threshold, setThreshold] = useState<number>(ts?.threshold ?? 1);
  // Response-time fields.
  const [thresholdMs, setThresholdMs] = useState<number>(ls?.threshold_ms ?? 1000);
  const [aggregation, setAggregation] = useState<"p95" | "max">(ls?.aggregation === "max" ? "max" : "p95");
  // Low-traffic floor.
  const [volumeThreshold, setVolumeThreshold] = useState<number>(vs?.threshold ?? 5);
  const [windowSec, setWindowSec] = useState<number>(ts?.window_seconds ?? ls?.window_seconds ?? vs?.window_seconds ?? 300);
  // Failed traces + low-traffic default to require-ack; response-time recovers
  // on its own (the latency drops back below threshold), so it defaults to auto.
  const [resolveMode, setResolveMode] = useState<ResolveMode>(
    rule ? (rule.resolve_mode === "auto" ? "auto" : "manual") : ls ? "auto" : "manual",
  );
  const [saving, setSaving] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // Switching mode on a NEW rule nudges the resolve default to match the
  // flavour (auto for latency, manual for errors/low-traffic). Never override on edit.
  const pickMode = (m: TraceMode) => {
    setMode(m);
    if (!rule) setResolveMode(m === "latency" ? "auto" : "manual");
  };

  const save = async () => {
    setSaving(true);
    setErr(null);
    const common = {
      severity,
      runbook: runbook.trim(),
      resolve_mode: resolveMode,
      signal: "trace" as const,
      ...scopeBinding(scope, target),
    };
    const body =
      mode === "latency"
        ? {
            ...common,
            name: name.trim() || "Response time",
            trace_latency_spec: {
              threshold_ms: Math.max(1, Math.floor(thresholdMs || 1)),
              window_seconds: windowSec,
              aggregation,
            },
          }
        : mode === "volume"
          ? {
              ...common,
              name: name.trim() || "Low traffic",
              trace_volume_spec: {
                threshold: Math.max(1, Math.floor(volumeThreshold || 1)),
                window_seconds: windowSec,
              },
            }
          : {
              ...common,
              name: name.trim() || "Failed traces",
              trace_error_spec: {
                threshold: Math.max(1, Math.floor(threshold || 1)),
                window_seconds: windowSec,
              },
            };
    try {
      if (rule) {
        await api.updateAlertRule(rule.id, {
          ...body, description: rule.description, enabled: rule.enabled,
          channel_ids: rule.channel_ids, group_id: rule.group_id,
          title_template: rule.title_template, body_template: rule.body_template,
        });
      } else {
        await api.createAlertRule({ ...body, enabled: true, channel_ids: [] });
      }
      onSaved();
    } catch (e) {
      setErr(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
      <div className="m-field">
        <label className="m-field-label">Condition</label>
        <div className="m-seg">
          <button type="button" className={`m-seg-b ${mode === "errors" ? "on" : ""}`} onClick={() => pickMode("errors")}>
            Failed traces
          </button>
          <button type="button" className={`m-seg-b ${mode === "latency" ? "on" : ""}`} onClick={() => pickMode("latency")}>
            Response time
          </button>
          <button type="button" className={`m-seg-b ${mode === "volume" ? "on" : ""}`} onClick={() => pickMode("volume")}>
            Low traffic
          </button>
        </div>
      </div>

      <div className="m-field">
        <label className="m-field-label">Name</label>
        <input className="search__input" value={name} onChange={(e) => setName(e.target.value)} placeholder={mode === "latency" ? "e.g. Slow checkout" : mode === "volume" ? "e.g. Orders stopped" : "e.g. Failed orders"} />
      </div>

      <div className="m-field">
        <label className="m-field-label">Runbook <span className="muted" style={{ fontWeight: 400 }}>optional</span></label>
        <textarea
          className="search__input"
          style={{ minHeight: 68, resize: "vertical", fontFamily: "inherit" }}
          value={runbook}
          onChange={(e) => setRunbook(e.target.value)}
          placeholder="What to do when this fires — e.g. Check the consumer group first; the usual cause is a stuck partition. Escalate to #payments-oncall after 15 minutes."
        />
        <div className="muted" style={{ fontSize: 11.5, marginTop: 4 }}>
          Sent with every notification and available to agents over MCP, so whoever picks this
          up gets your playbook instead of guessing.
        </div>
      </div>

      {mode === "latency" ? (
        <>
          <div className="m-rule-sentence">
            <span className="m-rs-prose">When</span>
            <select className="m-rs-sel" value={aggregation} onChange={(e) => setAggregation(e.target.value as "p95" | "max")}>
              <option value="p95">p95</option>
              <option value="max">max</option>
            </select>
            <span className="m-rs-prose">response time ≥</span>
            <input className="m-rs-num" type="number" min={1} value={thresholdMs} onChange={(e) => setThresholdMs(Number(e.target.value))} />
            <span className="m-rs-prose">ms over</span>
            <select className="m-rs-sel" value={windowSec} onChange={(e) => setWindowSec(Number(e.target.value))}>
              {WINDOW_CHOICES.map((w) => <option key={w.seconds} value={w.seconds}>{w.label}</option>)}
            </select>
          </div>
          <p className="muted" style={{ fontSize: 12, margin: 0 }}>
            Span durations on this {scope}'s services are aggregated over the window; {aggregation === "max" ? "the slowest span" : "the p95"} is compared to the threshold.
          </p>
        </>
      ) : mode === "volume" ? (
        <>
          <div className="m-rule-sentence">
            <span className="m-rs-prose">When fewer than</span>
            <input className="m-rs-num" type="number" min={1} value={volumeThreshold} onChange={(e) => setVolumeThreshold(Number(e.target.value))} />
            <span className="m-rs-prose">trace{volumeThreshold === 1 ? "" : "s"} in</span>
            <select className="m-rs-sel" value={windowSec} onChange={(e) => setWindowSec(Number(e.target.value))}>
              {WINDOW_CHOICES.map((w) => <option key={w.seconds} value={w.seconds}>{w.label}</option>)}
            </select>
          </div>
          <p className="muted" style={{ fontSize: 12, margin: 0 }}>
            Total traces on this {scope}'s services are counted over the window — zero (a fully silent {scope}) counts as below, so this catches a pipeline that has gone quiet.
          </p>
        </>
      ) : (
        <>
          <div className="m-rule-sentence">
            <span className="m-rs-prose">When ≥</span>
            <input className="m-rs-num" type="number" min={1} value={threshold} onChange={(e) => setThreshold(Number(e.target.value))} />
            <span className="m-rs-prose">failed trace{threshold === 1 ? "" : "s"} in</span>
            <select className="m-rs-sel" value={windowSec} onChange={(e) => setWindowSec(Number(e.target.value))}>
              {WINDOW_CHOICES.map((w) => <option key={w.seconds} value={w.seconds}>{w.label}</option>)}
            </select>
          </div>
          <p className="muted" style={{ fontSize: 12, margin: 0 }}>
            A failed trace is one with at least one error span on this {scope}'s services.
          </p>
        </>
      )}

      <div className="m-field">
        <label className="m-field-label">Severity</label>
        <div className="m-seg">
          {SEVERITIES.map((s) => (
            <button key={s.v} type="button" className={`m-seg-b sev-${s.v} ${severity === s.v ? "on" : ""}`} onClick={() => setSeverity(s.v)}>
              {s.label}
            </button>
          ))}
        </div>
      </div>

      <ResolveModeField value={resolveMode} onChange={setResolveMode} />

      {err && <div className="alert alert--error" style={{ margin: 0 }}>{err}</div>}

      <div className="m-builder-actions">
        <button className="btn btn--primary" type="button" disabled={saving} onClick={save}>
          {saving ? "Saving…" : rule ? "Save changes" : "Add health check"}
        </button>
        <button className="btn" type="button" onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}
