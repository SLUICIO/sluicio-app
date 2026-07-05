// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationSettings — the per-integration configuration page mounted
// at /integrations/:id/settings. v1 carries the Trace Completion rules
// surface (the SLA we just built). More rule types will land in
// follow-up commits.
//
// Trace completion rules answer: "is this integration finishing its
// traces?" A rule defines:
//   - one or more closing span names (e.g. "Klart", "OrderComplete")
//   - a timeout — if no closing span within N seconds of the first
//     span on the trace, the trace is delayed
//   - a lookback — how far back the evaluator scans (defaults to 4×
//     the timeout if not provided)
//   - severity + channel routing — same channel pool as metric alerts
//
// Sticky delayed: once a trace flips to delayed, the firing stays
// open in alert_instances even if the closing span eventually arrives.
// That matches "you missed the SLA" semantic.

import { FormEvent, useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { api } from "../api/client";
import IntegrationPageHeader from "../components/IntegrationPageHeader";
import PublicBadgeControl from "../components/PublicBadgeControl";
import MatcherConfig from "../components/MatcherConfig";
import { EditDrawer } from "../components/primitives";
import type {
  AlertRule,
  IntegrationDetail,
  NotificationChannel,
  TraceCompletionRule,
  TraceCompletionRuleInput,
} from "../api/types";
import { alertCondition, alertSignalLabel } from "../lib/alertRule";
import IntegrationProfileSelect from "../components/IntegrationProfileSelect";
import { useCurrentUser } from "../lib/useCurrentUser";
import ResourceGroupsCard from "../components/ResourceGroupsCard";
import ResourceSharesCard from "../components/ResourceSharesCard";
import { usePageTitle } from "../lib/usePageTitle";
import { useTimeWindow } from "../lib/useTimeWindow";

type TimeUnit = "seconds" | "minutes" | "hours";

// secondsAs converts a raw second-count to (n, unit) where n is the
// largest whole-number quotient. Used to render the existing rule in
// the unit the operator probably meant.
function secondsAs(s: number): { n: number; unit: TimeUnit } {
  if (s % 3600 === 0) return { n: s / 3600, unit: "hours" };
  if (s % 60 === 0) return { n: s / 60, unit: "minutes" };
  return { n: s, unit: "seconds" };
}

function toSeconds(n: number, unit: TimeUnit): number {
  switch (unit) {
    case "hours":   return n * 3600;
    case "minutes": return n * 60;
    case "seconds": return n;
  }
}

export default function IntegrationSettings() {
  const { id = "" } = useParams();
  const { can } = useCurrentUser();
  const isAdmin = can("org.manage");
  const [windowVal] = useTimeWindow();

  const [integration, setIntegration] = useState<IntegrationDetail | null>(null);
  // Scoped manage (RBAC v2): server-computed per integration.
  const canWrite = integration?.can_manage ?? can("integration.write");
  const [rules, setRules] = useState<TraceCompletionRule[]>([]);
  const [alertRules, setAlertRules] = useState<AlertRule[]>([]);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<TraceCompletionRule | "new" | null>(null);

  usePageTitle(integration ? `${integration.integration.name} · Settings` : "Integration settings");

  const refresh = () => {
    if (!id) return;
    Promise.all([
      api.getIntegration(id, windowVal),
      api.listTraceCompletionRules(id),
      api.listAlertRules({ integration: id }),
      api.listChannels(),
    ])
      .then(([detail, ruleResp, alertResp, chResp]) => {
        setIntegration(detail);
        setRules(ruleResp.rules);
        setAlertRules(alertResp.rules ?? []);
        setChannels(chResp.channels);
      })
      .catch((e) => setError(String((e as Error).message ?? e)));
  };
  useEffect(refresh, [id, windowVal]);

  if (error) return <div className="alert alert--error">Failed to load: {error}</div>;
  if (!integration) return <div className="placeholder">Loading…</div>;

  const onDelete = async (rule: TraceCompletionRule) => {
    if (!confirm(`Delete trace-completion rule "${rule.name}"?`)) return;
    try {
      await api.deleteTraceCompletionRule(id, rule.id);
      refresh();
    } catch (e) {
      alert(String((e as Error).message ?? e));
    }
  };

  return (
    <div className="flex flex-col gap-6">
      <IntegrationPageHeader detail={integration} />
      {/* Edit view: the tab strip is intentionally hidden (mirrors the
          service editor) so this reads as a focused settings page. */}
      <div>
        <Link className="btn ghost" to={`/integrations/${encodeURIComponent(id)}`}>← Back to integration</Link>
      </div>

      <IntegrationDetailsEditor
        integrationId={id}
        name={integration.integration.name}
        description={integration.integration.description}
        canWrite={canWrite}
        onSaved={refresh}
      />

      <MatcherConfig
        integrationId={id}
        data={integration}
        canWrite={canWrite}
        windowVal={windowVal}
        onChanged={refresh}
      />

      <section
        className="overflow-hidden rounded-lg border bg-surface-2"
        style={{ borderColor: "var(--border)" }}
      >
        <div className="flex items-start justify-between gap-4 border-b border-border px-4 py-3">
          <div>
            <h2 className="text-base font-semibold">Trace completion rules</h2>
            <p className="text-xs text-muted mt-1">
              A rule starts from a <b>start span</b> (only traces that
              emit it are evaluated and counted as this integration's
              messages) and walks an ordered chain of stages. Each stage
              must arrive within its timeout of the previous one;
              whichever hop runs late marks the trace delayed and fires
              the integration's alert channels.
            </p>
          </div>
          {isAdmin && (
            <button
              type="button"
              className="btn btn--primary"
              style={{ flexShrink: 0 }}
              onClick={() => setEditing("new")}
            >
              + New rule
            </button>
          )}
        </div>
        <div className="p-4">
          {rules.length === 0 ? (
            <div className="placeholder">
              No trace-completion rules yet.{" "}
              {isAdmin
                ? <>Click <b>+ New rule</b> to define one.</>
                : "Ask an org admin to define one."}
            </div>
          ) : (
            <table className="table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Pipeline</th>
                  <th>Severity</th>
                  <th>Channels</th>
                  <th>Enabled</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {rules.map((r) => {
                  return (
                    <tr key={r.id}>
                      <td>
                        <div style={{ fontWeight: 600 }}>{r.name}</div>
                        {r.description && (
                          <div className="muted" style={{ fontSize: 12 }}>{r.description}</div>
                        )}
                      </td>
                      <td>
                        <RulePipeline rule={r} />
                      </td>
                      <td><span className={`badge sev-${r.severity}`}>{r.severity}</span></td>
                      <td>{(r.channel_ids ?? []).length}</td>
                      <td>{r.enabled ? "✓" : "—"}</td>
                      <td className="num">
                        {isAdmin && (
                          <>
                            <button type="button" className="btn btn--link" onClick={() => setEditing(r)}>
                              Edit
                            </button>
                            <button
                              type="button"
                              className="btn btn--link"
                              style={{ color: "var(--err-ink, #ef4444)" }}
                              onClick={() => onDelete(r)}
                            >
                              Delete
                            </button>
                          </>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      </section>

      <section
        className="overflow-hidden rounded-lg border bg-surface-2"
        style={{ borderColor: "var(--border)" }}
      >
        <div className="border-b border-border px-4 py-3">
          <h2 className="text-base font-semibold">Notification profile</h2>
          <p className="text-xs text-muted mt-1">
            The profile used for this integration's alerts and unacknowledged error traces —
            it decides the channels, grouping, and re-notify interval. Leave on “Inherit”
            to use the owning team's default, then the org-wide default. Manage profiles
            per team in Settings → Groups and org-wide on the <Link to="/alerts">Alerts</Link> page.
          </p>
        </div>
        <div className="p-4">
          <IntegrationProfileSelect integrationId={id} canWrite={canWrite} />
        </div>
      </section>

      <ResourceGroupsCard kind="integration" id={id} />
      <ResourceSharesCard kind="integrations" id={id} canManage={canWrite} />

      <section
        className="overflow-hidden rounded-lg border bg-surface-2"
        style={{ borderColor: "var(--border)" }}
      >
        <div className="border-b border-border px-4 py-3">
          <h2 className="text-base font-semibold">Public status badge</h2>
          <p className="text-xs text-muted mt-1">
            Serve a shields-style health badge for this integration at a public URL — embed
            it in a README like a CI badge. Off by default.
          </p>
        </div>
        <div className="p-4">
          <PublicBadgeControl
            kind="integration"
            id={integration.integration.id}
            enabled={integration.integration.badge_public ?? false}
            canManage={canWrite}
            onChange={refresh}
          />
        </div>
      </section>

      <IntegrationAlertRules
        rules={alertRules}
        channels={channels}
        canWrite={canWrite}
        onChanged={refresh}
        onError={setError}
      />

      {editing && (
        <EditDrawer
          title={editing === "new" ? "New trace-completion rule" : `Edit “${editing.name}”`}
          width="wide"
          onClose={() => setEditing(null)}
        >
          <RuleForm
            integrationID={id}
            rule={editing === "new" ? null : editing}
            channels={channels}
            onClose={() => setEditing(null)}
            onSaved={() => {
              setEditing(null);
              refresh();
            }}
          />
        </EditDrawer>
      )}
    </div>
  );
}

// ── form ──────────────────────────────────────────────────────────────

// fmtSeconds renders a second-count compactly (e.g. "5m", "30s", "2h").
function fmtSeconds(s: number): string {
  const { n, unit } = secondsAs(s);
  const suffix = unit === "hours" ? "h" : unit === "minutes" ? "m" : "s";
  return `${n}${suffix}`;
}

// RulePipeline renders a rule's start span + ordered stages as a chain
// of badges with each hop's effective timeout.
function RulePipeline({ rule }: { rule: TraceCompletionRule }) {
  const def = rule.default_timeout_seconds || rule.timeout_seconds || 0;
  const stages =
    rule.stages && rule.stages.length > 0
      ? rule.stages
      : [{ span_names: rule.closing_span_names ?? [], timeout_seconds: rule.timeout_seconds }];
  return (
    <div style={{ display: "flex", flexWrap: "wrap", alignItems: "center", gap: 4 }}>
      {rule.start_span_name ? (
        <span className="badge mono">{rule.start_span_name}</span>
      ) : (
        <span className="muted" style={{ fontSize: 12 }}>any start</span>
      )}
      {stages.map((st, i) => {
        const t = st.timeout_seconds && st.timeout_seconds > 0 ? st.timeout_seconds : def;
        return (
          <span key={i} style={{ display: "inline-flex", alignItems: "center", gap: 4 }}>
            <span className="muted" style={{ fontSize: 12 }}>→</span>
            <span className="badge mono">{(st.span_names ?? []).join(" / ")}</span>
            {t > 0 && (
              <span className="muted" style={{ fontSize: 11 }}>({fmtSeconds(t)})</span>
            )}
          </span>
        );
      })}
    </div>
  );
}

// StageDraft is the editor's in-memory shape for one pipeline hop.
interface StageDraft {
  spans: string; // comma-separated span names
  inherit: boolean; // use the rule's default timeout
  timeoutN: number;
  timeoutUnit: TimeUnit;
}

function stageDraftsFromRule(rule: TraceCompletionRule | null): StageDraft[] {
  const src =
    rule?.stages && rule.stages.length > 0
      ? rule.stages
      : rule
        ? [{ span_names: rule.closing_span_names ?? ["Klart"], timeout_seconds: rule.timeout_seconds }]
        : [{ span_names: ["Klart"], timeout_seconds: 0 }];
  return src.map((st) => {
    const explicit = !!st.timeout_seconds && st.timeout_seconds > 0;
    const t = explicit ? secondsAs(st.timeout_seconds!) : { n: 5, unit: "minutes" as TimeUnit };
    return {
      spans: (st.span_names ?? []).join(", "),
      inherit: !explicit,
      timeoutN: t.n,
      timeoutUnit: t.unit,
    };
  });
}

function RuleForm({
  integrationID,
  rule,
  channels,
  onClose,
  onSaved,
}: {
  integrationID: string;
  rule: TraceCompletionRule | null;
  channels: NotificationChannel[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const defInit = rule
    ? secondsAs(rule.default_timeout_seconds || rule.timeout_seconds || 300)
    : { n: 5, unit: "minutes" as TimeUnit };
  const [name, setName] = useState(rule?.name ?? "");
  const [description, setDescription] = useState(rule?.description ?? "");
  const [severity, setSeverity] = useState<TraceCompletionRule["severity"]>(rule?.severity ?? "warning");
  const [enabled, setEnabled] = useState(rule?.enabled ?? true);
  const [startSpan, setStartSpan] = useState<string>(rule?.start_span_name ?? "");
  const [stages, setStages] = useState<StageDraft[]>(() => stageDraftsFromRule(rule));
  const [defTimeoutN, setDefTimeoutN] = useState<number>(defInit.n);
  const [defTimeoutUnit, setDefTimeoutUnit] = useState<TimeUnit>(defInit.unit);
  const [channelIDs, setChannelIDs] = useState<string[]>(rule?.channel_ids ?? []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Span names seen across this integration's traces — offered as
  // datalist suggestions on the start/stage span inputs. Empty (new or
  // quiet integration) → the inputs are plain free text.
  const [spanSuggestions, setSpanSuggestions] = useState<string[]>([]);
  const spanListId = `span-names-${integrationID}`;

  useEffect(() => {
    let active = true;
    api
      .integrationSpanNames(integrationID)
      .then((r) => active && setSpanSuggestions(r.span_names ?? []))
      .catch(() => active && setSpanSuggestions([]));
    return () => {
      active = false;
    };
  }, [integrationID]);

  const updateStage = (idx: number, patch: Partial<StageDraft>) => {
    setStages((prev) => prev.map((s, i) => (i === idx ? { ...s, ...patch } : s)));
  };
  const addStage = () =>
    setStages((prev) => [...prev, { spans: "", inherit: true, timeoutN: 5, timeoutUnit: "minutes" }]);
  const removeStage = (idx: number) =>
    setStages((prev) => prev.filter((_, i) => i !== idx));
  const moveStage = (idx: number, dir: -1 | 1) =>
    setStages((prev) => {
      const next = [...prev];
      const j = idx + dir;
      if (j < 0 || j >= next.length) return prev;
      [next[idx], next[j]] = [next[j], next[idx]];
      return next;
    });

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const defaultTimeoutSeconds = toSeconds(defTimeoutN, defTimeoutUnit);
    const builtStages = stages
      .map((s) => {
        const spanNames = s.spans
          .split(",")
          .map((x) => x.trim())
          .filter(Boolean);
        const stage: { span_names: string[]; timeout_seconds?: number } = { span_names: spanNames };
        if (!s.inherit) stage.timeout_seconds = toSeconds(s.timeoutN, s.timeoutUnit);
        return stage;
      })
      .filter((s) => s.span_names.length > 0);
    if (builtStages.length === 0) {
      setError("Add at least one stage with a span name.");
      setBusy(false);
      return;
    }
    const longest = Math.max(
      defaultTimeoutSeconds,
      ...builtStages.map((s) => s.timeout_seconds ?? defaultTimeoutSeconds),
    );
    const body: TraceCompletionRuleInput = {
      name: name.trim(),
      description: description.trim(),
      severity,
      enabled,
      start_span_name: startSpan.trim(),
      stages: builtStages,
      default_timeout_seconds: defaultTimeoutSeconds,
      lookback_seconds: longest * 4, // ≥ 2× longest hop; server clamps too
      channel_ids: channelIDs,
    };
    try {
      if (rule) {
        await api.updateTraceCompletionRule(integrationID, rule.id, body);
      } else {
        await api.createTraceCompletionRule(integrationID, body);
      }
      onSaved();
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setBusy(false);
    }
  };

  const toggleChannel = (id: string) => {
    setChannelIDs((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]
    );
  };

  return (
    <form onSubmit={submit} className="form">
      <div className="form__row">
        <label className="form__label">
          Name
          <input
            className="search__input"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Order processing must complete"
            required
            autoFocus
          />
        </label>
        <label className="form__label">
          Severity
          <select
            className="toolbar__select"
            value={severity}
            onChange={(e) => setSeverity(e.target.value as TraceCompletionRule["severity"])}
          >
            <option value="info">info</option>
            <option value="warning">warning</option>
            <option value="critical">critical</option>
          </select>
        </label>
      </div>

      <label className="form__label">
        Description (optional)
        <input
          className="search__input"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="What this SLA is for"
        />
      </label>

      {/* Suggestions sourced from the integration's actual traces;
          shared by the start span + every stage input. Empty datalist =
          plain free text. */}
      <datalist id={spanListId}>
        {spanSuggestions.map((s) => (
          <option key={s} value={s} />
        ))}
      </datalist>

      <label className="form__label">
        Start span
        <input
          className="search__input mono"
          list={spanListId}
          value={startSpan}
          onChange={(e) => setStartSpan(e.target.value)}
          placeholder="Start"
        />
        <span className="form__hint">
          {spanSuggestions.length > 0
            ? "Pick from spans seen on this integration, or type one. "
            : ""}
          Only traces that emit this span are evaluated and counted as
          this integration's messages. Leave empty to evaluate every
          trace on the integration's services (legacy behaviour).
        </span>
      </label>

      <div className="form__label">
        <span>Default stage timeout</span>
        <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
          <input
            type="number"
            min={1}
            className="search__input mono"
            style={{ width: 100, textAlign: "right" }}
            value={defTimeoutN}
            onChange={(e) => setDefTimeoutN(Math.max(1, parseInt(e.target.value, 10) || 0))}
            required
          />
          <select
            className="toolbar__select"
            value={defTimeoutUnit}
            onChange={(e) => setDefTimeoutUnit(e.target.value as TimeUnit)}
          >
            <option value="seconds">seconds</option>
            <option value="minutes">minutes</option>
            <option value="hours">hours</option>
          </select>
        </div>
        <span className="form__hint">
          Used by any stage left on "default". Each stage's clock starts
          from the previous stage's span (or the start span, for the
          first stage).
        </span>
      </div>

      <div className="form__label">
        <span>Stages</span>
        <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
          {stages.map((s, i) => (
            <div
              key={i}
              style={{
                display: "flex",
                gap: 6,
                alignItems: "center",
                flexWrap: "wrap",
                padding: 8,
                border: "1px solid var(--border)",
                borderRadius: 6,
              }}
            >
              <span className="muted mono" style={{ fontSize: 12, minWidth: 18 }}>{i + 1}.</span>
              <input
                className="search__input mono"
                list={spanListId}
                style={{ flex: "1 1 180px" }}
                value={s.spans}
                onChange={(e) => updateStage(i, { spans: e.target.value })}
                placeholder={i === 0 ? "Klart" : "To be done"}
              />
              <label style={{ display: "flex", alignItems: "center", gap: 4, fontSize: 12 }}>
                <input
                  type="checkbox"
                  checked={s.inherit}
                  onChange={(e) => updateStage(i, { inherit: e.target.checked })}
                />
                default timeout
              </label>
              {!s.inherit && (
                <div style={{ display: "flex", gap: 4, alignItems: "center" }}>
                  <input
                    type="number"
                    min={1}
                    className="search__input mono"
                    style={{ width: 72, textAlign: "right" }}
                    value={s.timeoutN}
                    onChange={(e) => updateStage(i, { timeoutN: Math.max(1, parseInt(e.target.value, 10) || 0) })}
                  />
                  <select
                    className="toolbar__select"
                    value={s.timeoutUnit}
                    onChange={(e) => updateStage(i, { timeoutUnit: e.target.value as TimeUnit })}
                  >
                    <option value="seconds">s</option>
                    <option value="minutes">m</option>
                    <option value="hours">h</option>
                  </select>
                </div>
              )}
              <div style={{ marginLeft: "auto", display: "flex", gap: 2 }}>
                <button type="button" className="btn btn--link" disabled={i === 0} onClick={() => moveStage(i, -1)} title="Move up">↑</button>
                <button type="button" className="btn btn--link" disabled={i === stages.length - 1} onClick={() => moveStage(i, 1)} title="Move down">↓</button>
                <button
                  type="button"
                  className="btn btn--link"
                  style={{ color: "var(--err-ink, #ef4444)" }}
                  disabled={stages.length <= 1}
                  onClick={() => removeStage(i)}
                  title="Remove stage"
                >
                  ✕
                </button>
              </div>
            </div>
          ))}
        </div>
        <button type="button" className="btn" style={{ alignSelf: "flex-start", marginTop: 6 }} onClick={addStage}>
          + Add stage
        </button>
        <span className="form__hint">
          Each stage is one or more span names (comma-separated; any one
          satisfies the hop). The trace is delayed at whichever stage
          runs past its timeout. Match is case-sensitive.
        </span>
      </div>

      <label className="form__label" style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
        <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
        <span>Enabled</span>
      </label>

      <div className="form__label">
        <span>Alert channels</span>
        {channels.length === 0 ? (
          <p className="muted" style={{ fontSize: 12 }}>
            No notification channels configured.{" "}
            <Link to="/alerts">Configure channels →</Link>
          </p>
        ) : (
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            {channels.map((c) => (
              <label key={c.id} style={{ display: "flex", gap: 8, alignItems: "center" }}>
                <input
                  type="checkbox"
                  checked={channelIDs.includes(c.id)}
                  onChange={() => toggleChannel(c.id)}
                />
                <span>
                  <b>{c.name}</b> <span className="muted">· {c.kind}</span>
                </span>
              </label>
            ))}
          </div>
        )}
        <span className="form__hint">
          Same channel pool as metric alerts. A delayed-trace firing
          routes through the same Slack / PagerDuty / webhook delivery.
        </span>
      </div>

      {error && <div className="alert alert--error">{error}</div>}

      <div className="form__actions">
        <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
        <button type="submit" className="btn btn--primary" disabled={busy || !name.trim()}>
          {busy ? "Saving…" : rule ? "Save changes" : "Create rule"}
        </button>
      </div>
    </form>
  );
}

// IntegrationAlertRules lists the alert_rules bound to this integration —
// the failed-trace rules created from the Errors breakdown, plus any
// metric/log rules scoped to it. These fire through the same delivery
// pipeline as everything on the Alerts page; surfacing them here (next to
// the trace-completion rules) means an integration's alerting is fully
// visible and manageable in one place, rather than silently living only
// on the global Alerts page. Enable/disable + delete mirror that page;
// full editing (and creating failed-trace rules) happens from the Errors
// breakdown and the Metrics/Logs explorers.
function IntegrationAlertRules({
  rules,
  channels,
  canWrite,
  onChanged,
  onError,
}: {
  rules: AlertRule[];
  channels: NotificationChannel[];
  canWrite: boolean;
  onChanged: () => void;
  onError: (e: string) => void;
}) {
  const channelName = (cid: string) =>
    channels.find((c) => c.id === cid)?.name ?? "—";

  const toggle = async (rule: AlertRule) => {
    try {
      // Replace ALL mutable fields — the update is a full PUT, so route
      // the spec by signal or a log/trace rule would be re-validated as
      // an empty metric rule and rejected.
      const signal = rule.signal === "log" ? "log" : rule.signal === "trace" ? "trace" : "metric";
      await api.updateAlertRule(rule.id, {
        name: rule.name,
        description: rule.description,
        severity: rule.severity,
        enabled: !rule.enabled,
        channel_ids: rule.channel_ids,
        signal,
        spec: signal === "metric" ? rule.spec : undefined,
        log_spec: signal === "log" ? rule.log_spec : undefined,
        trace_error_spec: signal === "trace" ? rule.trace_error_spec : undefined,
        service_name: rule.service_name,
        integration_id: rule.integration_id,
        group_id: rule.group_id,
      });
      onChanged();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    }
  };

  const remove = async (rule: AlertRule) => {
    if (!confirm(`Delete alert rule "${rule.name}"?`)) return;
    try {
      await api.deleteAlertRule(rule.id);
      onChanged();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    }
  };

  return (
    <section
      className="overflow-hidden rounded-lg border bg-surface-2"
      style={{ borderColor: "var(--border)" }}
    >
      <div className="border-b border-border px-4 py-3">
        <h2 className="text-base font-semibold">Alert rules</h2>
        <p className="text-xs text-muted mt-1">
          Rules bound to this integration — failed-trace alerts created from
          the <b>Errors</b> breakdown, plus any metric or log rules scoped
          here. They notify through the same channels as everything on the{" "}
          <Link to="/alerts">Alerts</Link> page. Create failed-trace rules
          from the Errors breakdown; create metric/log rules from the{" "}
          <Link to="/metrics">Metrics</Link> and <Link to="/logs">Logs</Link>{" "}
          explorers.
        </p>
      </div>
      <div className="p-4">
        {rules.length === 0 ? (
          <div className="placeholder">
            No alert rules bound to this integration yet. Open the{" "}
            <b>Errors</b> tab and use “Alert on failed traces” to add one.
          </div>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Condition</th>
                <th>Severity</th>
                <th>Channels</th>
                <th>Status</th>
                {canWrite && <th></th>}
              </tr>
            </thead>
            <tbody>
              {rules.map((rule) => (
                <tr key={rule.id}>
                  <td>
                    <div style={{ fontWeight: 600 }}>{rule.name}</div>
                    {rule.description && (
                      <div className="muted" style={{ fontSize: 12 }}>{rule.description}</div>
                    )}
                  </td>
                  <td className="mono" style={{ fontSize: 12 }}>
                    {alertSignalLabel(rule.signal) && (
                      <span className="badge" style={{ marginRight: 6, fontSize: 10 }}>
                        {alertSignalLabel(rule.signal)}
                      </span>
                    )}
                    {alertCondition(rule)}
                  </td>
                  <td><span className={`badge sev-${rule.severity}`}>{rule.severity}</span></td>
                  <td className="muted" style={{ fontSize: 12 }}>
                    {rule.channel_ids.length === 0 ? "—" : rule.channel_ids.map(channelName).join(", ")}
                  </td>
                  <td>
                    {canWrite ? (
                      <button className="btn btn--sm" type="button" onClick={() => toggle(rule)}>
                        {rule.enabled ? "Enabled" : "Disabled"}
                      </button>
                    ) : (
                      <span className="muted" style={{ fontSize: 12 }}>
                        {rule.enabled ? "Enabled" : "Disabled"}
                      </span>
                    )}
                  </td>
                  {canWrite && (
                    <td className="num">
                      <button
                        type="button"
                        className="btn btn--link"
                        style={{ color: "var(--err-ink, #ef4444)" }}
                        onClick={() => remove(rule)}
                      >
                        Delete
                      </button>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </section>
  );
}

// IntegrationDetailsEditor — rename an integration / edit its description.
// The slug (used in URLs and as the stable identifier) is fixed at
// creation, so it isn't editable here. Editing is gated on integration
// write permission; the cell-api enforces the same.
function IntegrationDetailsEditor({
  integrationId,
  name: initialName,
  description: initialDescription,
  canWrite,
  onSaved,
}: {
  integrationId: string;
  name: string;
  description: string;
  canWrite: boolean;
  onSaved: () => void;
}) {
  const [name, setName] = useState(initialName);
  const [description, setDescription] = useState(initialDescription ?? "");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState(0);

  // Re-seed when the parent reloads the integration (e.g. after save →
  // refresh), so the fields track server state rather than going stale.
  useEffect(() => {
    setName(initialName);
    setDescription(initialDescription ?? "");
  }, [initialName, initialDescription]);

  const trimmedName = name.trim();
  const valid = trimmedName.length > 0;
  const dirty =
    trimmedName !== initialName || description.trim() !== (initialDescription ?? "").trim();

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (!valid || !dirty) return;
    setSaving(true);
    setError(null);
    try {
      await api.updateIntegration(integrationId, {
        name: trimmedName,
        description: description.trim(),
      });
      setSavedAt(Date.now());
      onSaved();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <section
      className="overflow-hidden rounded-lg border bg-surface-2"
      style={{ borderColor: "var(--border)" }}
    >
      <div className="border-b border-border px-4 py-3">
        <h2 className="text-base font-semibold">Integration details</h2>
        <p className="text-xs text-muted mt-1">
          The name and description shown across Sluicio. The slug (used in URLs)
          is fixed once the integration is created.
        </p>
      </div>
      <form
        onSubmit={save}
        className="p-4"
        style={{ display: "flex", flexDirection: "column", gap: 12, maxWidth: 560 }}
      >
        {error && <div className="alert alert--error">{error}</div>}
        <label className="form__label">
          Name
          <input
            className="search__input"
            value={name}
            maxLength={120}
            disabled={!canWrite}
            onChange={(e) => setName(e.target.value)}
            placeholder="Order Management"
          />
        </label>
        <label className="form__label">
          Description
          <textarea
            className="svc-textarea"
            rows={3}
            value={description}
            disabled={!canWrite}
            onChange={(e) => setDescription(e.target.value)}
            placeholder="What this integration does (optional)."
          />
        </label>
        {canWrite ? (
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <button
              type="submit"
              className="btn btn--primary"
              disabled={saving || !dirty || !valid}
            >
              {saving ? "Saving…" : "Save changes"}
            </button>
            {!valid && <span className="muted" style={{ fontSize: 12 }}>Name is required.</span>}
            {savedAt > 0 && !dirty && valid && (
              <span className="muted" style={{ fontSize: 12 }}>Saved.</span>
            )}
          </div>
        ) : (
          <p className="muted" style={{ fontSize: 12 }}>
            Your role doesn't allow editing this integration.
          </p>
        )}
      </form>
    </section>
  );
}
