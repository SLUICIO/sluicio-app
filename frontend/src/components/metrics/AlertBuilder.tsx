// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The alert-rule builder inside the metric drawer: a sentence-style rule
// editor ("When max of queue.depth is > 50 for 5m"), a live would-fire
// preview, a severity control, a notification-channel picker, and the
// existing rules already attached to the metric. The dimension filters
// carried from the search bar become the rule's scope.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../../api/client";
import SearchableSelect from "../SearchableSelect";
import AlertNotificationContent from "./AlertNotificationContent";
import type {
  AlertAggregation,
  AlertOperator,
  AlertPreview,
  AlertRule,
  AlertSeverity,
  Group,
  MetricRuleSpec,
  NotificationChannel,
  NotificationContent,
  RuleAttrFilter,
} from "../../api/types";

const AGGS: AlertAggregation[] = ["last", "max", "avg", "min", "sum", "p95", "increase", "rate", "age"];
// Friendly dropdown labels; the wire value stays the short form.
const AGG_LABELS: Record<AlertAggregation, string> = {
  last: "last value",
  max: "max",
  avg: "avg",
  min: "min",
  sum: "sum",
  p95: "p95",
  increase: "increase (counter Δ)",
  rate: "rate (per sec)",
  // "age" treats the metric's value as a Unix timestamp and thresholds
  // now − value in SECONDS — e.g. "file.mtime age > 3600" = file untouched
  // for over an hour. Pair with gt for a staleness health check.
  age: "age / time since (sec)",
};
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

const CHANNEL_GLYPH: Record<string, string> = { slack: "#", pagerduty: "⚠", webhook: "↗" };

export default function AlertBuilder({
  metricName,
  unit,
  attrs,
  onChanged,
  defaultService,
}: {
  metricName: string;
  unit?: string;
  attrs: RuleAttrFilter[];
  onChanged?: () => void;
  // When opened from a service's Metrics tab, pre-select that service as
  // the health-check target so a breach flips its pill by default.
  defaultService?: string;
}) {
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [services, setServices] = useState<string[]>([]);
  const [healthService, setHealthService] = useState(defaultService ?? "");
  // Owning team. "" = org-wide (visible to everyone). Non-admins can
  // only pick teams they belong to — the API enforces this too.
  const [groups, setGroups] = useState<Group[]>([]);
  const [team, setTeam] = useState("");
  // Optional notification templates (Go text/template). Blank = the
  // built-in auto-generated title/body.
  const [titleTpl, setTitleTpl] = useState("");
  const [bodyTpl, setBodyTpl] = useState("");
  const [notif, setNotif] = useState<NotificationContent>({});
  const [existing, setExisting] = useState<AlertRule[]>([]);
  const [name, setName] = useState(`${metricName} alert`);
  const [agg, setAgg] = useState<AlertAggregation>("max");
  const [op, setOp] = useState<AlertOperator>("gt");
  const [threshold, setThreshold] = useState<number>(50);
  const [forWindow, setForWindow] = useState("5m");
  const [splitBy, setSplitBy] = useState("");
  const [attrKeys, setAttrKeys] = useState<string[]>([]);
  const [severity, setSeverity] = useState<AlertSeverity>("warning");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [preview, setPreview] = useState<AlertPreview | null>(null);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState(0);

  const spec: MetricRuleSpec = useMemo(
    () => ({ metric_name: metricName, aggregation: agg, operator: op, threshold, for_window: forWindow, attrs, split_by: splitBy || undefined }),
    [metricName, agg, op, threshold, forWindow, attrs, splitBy],
  );

  const refreshRules = useCallback(() => {
    api
      .listAlertRules()
      .then((r) => setExisting((r.rules ?? []).filter((x) => x.spec.metric_name === metricName)))
      .catch(() => setExisting([]));
  }, [metricName]);

  // Channels + the metric's emitting services + existing rules; reset on
  // metric change.
  useEffect(() => {
    setName(`${metricName} alert`);
    setHealthService(defaultService ?? "");
    setSplitBy("");
    setTitleTpl("");
    setBodyTpl("");
    api.listChannels().then((r) => setChannels(r.channels ?? [])).catch(() => setChannels([]));
    api.listGroups().then((r) => setGroups(r.groups ?? [])).catch(() => setGroups([]));
    // Every service in the catalog — a health check can bind this metric
    // to any service, not just the ones currently emitting it (you may
    // want a service's pill driven by a gateway/queue metric it doesn't
    // emit itself).
    api
      .listServices("24h")
      .then((r) => setServices((r.services ?? []).map((s) => s.service_name).sort()))
      .catch(() => setServices([]));
    // Attribute keys on this metric — the split-by dimension options.
    api
      .metricFields("1h", metricName)
      .then((r) => setAttrKeys((r.fields ?? []).map((f) => f.key).sort()))
      .catch(() => setAttrKeys([]));
    refreshRules();
  }, [metricName, refreshRules, defaultService]);

  // Live would-fire preview, debounced as the rule changes.
  const debounce = useRef<number | undefined>(undefined);
  useEffect(() => {
    window.clearTimeout(debounce.current);
    setPreviewLoading(true);
    debounce.current = window.setTimeout(() => {
      api
        .previewAlertRule(spec, healthService || undefined)
        .then(setPreview)
        .catch(() => setPreview(null))
        .finally(() => setPreviewLoading(false));
    }, 350);
    return () => window.clearTimeout(debounce.current);
  }, [spec, healthService]);

  const toggleChannel = (id: string) =>
    setSelected((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });

  const create = async () => {
    setSaving(true);
    setError(null);
    try {
      await api.createAlertRule({
        name: name.trim() || `${metricName} alert`,
        severity,
        enabled: true,
        channel_ids: [...selected],
        spec,
        service_name: healthService || undefined,
        group_id: team || undefined,
        title_template: titleTpl.trim() || undefined,
        body_template: bodyTpl.trim() || undefined,
        notification_config: Object.values(notif).some(Boolean) ? notif : undefined,
      });
      setSavedAt(Date.now());
      refreshRules();
      onChanged?.();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  const removeRule = async (id: string) => {
    try {
      await api.deleteAlertRule(id);
      refreshRules();
      onChanged?.();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  const opGlyph = OPS.find((o) => o.op === op)?.glyph ?? op;

  return (
    <section>
      <div className="m-section-head">
        <span className="m-section-title">⚡ Add a health check or alert</span>
      </div>

      <div className="m-builder-card">
        {/* sentence-style rule */}
        <div className="m-rule-sentence">
          <span className="m-rs-prose">When</span>
          <select className="m-rs-sel" value={agg} onChange={(e) => setAgg(e.target.value as AlertAggregation)}>
            {AGGS.map((a) => (
              <option key={a} value={a}>{AGG_LABELS[a]}</option>
            ))}
          </select>
          <span className="m-rs-prose">of</span>
          <span className="m-rs-metric">{metricName}</span>
          <span className="m-rs-prose">is</span>
          <select className="m-rs-sel" value={op} onChange={(e) => setOp(e.target.value as AlertOperator)}>
            {OPS.map((o) => (
              <option key={o.op} value={o.op}>{o.glyph}</option>
            ))}
          </select>
          <input
            className="m-rs-num"
            type="number"
            value={threshold}
            onChange={(e) => setThreshold(Number(e.target.value))}
          />
          {unit && <span className="m-rs-prose muted">{unit}</span>}
          <span className="m-rs-prose">for</span>
          <select className="m-rs-sel" value={forWindow} onChange={(e) => setForWindow(e.target.value)}>
            {WINDOWS.map((wn) => (
              <option key={wn} value={wn}>{wn}</option>
            ))}
          </select>
        </div>

        {/* Add as a health check — bind this metric's threshold to a
            service so a breach flips its pill (and any integration it
            belongs to). Promoted to the top: turning a metric into a
            service health check is the most common reason to open this
            from the Metrics page. */}
        <div className="m-field m-healthcheck">
          <label className="m-field-label">♥ Add as a health check to a service</label>
          <SearchableSelect
            value={healthService}
            onChange={setHealthService}
            options={services}
            allLabel="Don't — just alert, leave service health unchanged"
            placeholder="Pick the service this metric reflects…"
          />
          <span className="muted" style={{ fontSize: 11.5 }}>
            {healthService
              ? `Whenever the threshold above is breached, ${healthService} reads as unhealthy — this becomes one of its health checks, and any integration it belongs to follows.`
              : "Optional — bind this metric to a service so it (and its integrations) reads unhealthy whenever the threshold above is breached."}
          </span>
        </div>

        {/* split-by: break the rule down per attribute value so the alert
            names each breaching value (e.g. each DLQ queue) instead of
            collapsing everything into one number. */}
        <div className="m-field">
          <label className="m-field-label">Break down by attribute</label>
          <select
            className="m-rs-sel"
            value={splitBy}
            onChange={(e) => setSplitBy(e.target.value)}
          >
            <option value="">Don't split — one combined value</option>
            {attrKeys.map((k) => (
              <option key={k} value={k}>{k}</option>
            ))}
          </select>
          <span className="muted" style={{ fontSize: 11.5 }}>
            {splitBy
              ? `Each distinct ${splitBy} is checked on its own; the alert lists every ${splitBy} that breaches and how many.`
              : "Optional — evaluate each value of an attribute (e.g. queue_name) separately so the alert shows which ones are unhealthy."}
          </span>
        </div>

        {/* live preview */}
        <PreviewBanner preview={preview} loading={previewLoading} op={opGlyph} unit={unit} splitBy={splitBy} />

        {/* severity */}
        <div className="m-field">
          <label className="m-field-label">Severity</label>
          <div className="m-seg">
            {SEVERITIES.map((s) => (
              <button
                key={s.v}
                type="button"
                className={`m-seg-b sev-${s.v} ${severity === s.v ? "on" : ""}`}
                onClick={() => setSeverity(s.v)}
              >
                {s.label}
              </button>
            ))}
          </div>
        </div>

        {/* channel picker */}
        <div className="m-field">
          <label className="m-field-label">Send to channel</label>
          {channels.length === 0 ? (
            <div className="muted" style={{ fontSize: 12 }}>
              No channels yet — add one on the <Link to="/alerts">Alerts</Link> page.
            </div>
          ) : (
            <div className="m-intg-list">
              {channels.map((c) => {
                const on = selected.has(c.id);
                return (
                  <button
                    key={c.id}
                    type="button"
                    className={`m-intg ${on ? "on" : ""}`}
                    onClick={() => toggleChannel(c.id)}
                  >
                    <span className="m-intg-icon">{CHANNEL_GLYPH[c.kind] ?? "•"}</span>
                    <div className="m-intg-mid">
                      <div className="m-intg-name">{c.name}</div>
                      <div className="m-intg-kind">{c.kind}</div>
                    </div>
                    <span className="m-intg-tick">{on ? "✓" : ""}</span>
                  </button>
                );
              })}
            </div>
          )}
        </div>

        {/* owning team — scopes who can see/edit this alert. Org-wide
            (default) is visible to everyone; a team restricts it to
            members + org admins. */}
        <div className="m-field">
          <label className="m-field-label">Team</label>
          <select
            className="m-rs-sel"
            value={team}
            onChange={(e) => setTeam(e.target.value)}
            disabled={groups.length === 0}
          >
            <option value="">Org-wide (everyone)</option>
            {groups.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
              </option>
            ))}
          </select>
          <span className="muted" style={{ fontSize: 11.5 }}>
            {team
              ? "Only members of this team (and org admins) will see and manage this alert."
              : groups.length === 0
                ? "No teams yet — create one in Settings → Groups to scope alerts."
                : "Optional — assign to a team to limit who can see and manage it."}
          </span>
        </div>

        {/* Notification content: which enrichment blocks the alert's email +
            webhook include, an optional inline Liquid email, and a preview. */}
        <details className="m-field" style={{ border: "1px solid var(--border)", borderRadius: 6, padding: "8px 10px" }}>
          <summary className="m-field-label" style={{ cursor: "pointer" }}>
            Notification content &amp; layout
          </summary>
          <div style={{ marginTop: 8 }}>
            <AlertNotificationContent value={notif} onChange={setNotif} />
          </div>
        </details>

        {/* custom notification layout — optional title + body
            templates. Blank uses the built-in auto summary. */}
        <details className="m-field" style={{ border: "1px solid var(--border)", borderRadius: 6, padding: "8px 10px" }}>
          <summary className="m-field-label" style={{ cursor: "pointer" }}>
            Advanced: legacy text templates (optional)
          </summary>
          <div style={{ display: "flex", flexDirection: "column", gap: 8, marginTop: 8 }}>
            <input
              className="search__input"
              style={{ fontSize: 13 }}
              placeholder="Title — e.g. [{{.severity}}] {{.rule_name}}"
              value={titleTpl}
              onChange={(e) => setTitleTpl(e.target.value)}
            />
            <textarea
              className="svc-textarea"
              style={{ fontSize: 13, minHeight: 72, fontFamily: "var(--font-mono, monospace)" }}
              placeholder={"Body — e.g.\n{{.metric}} is {{.value}} (threshold {{.threshold}}).\nState: {{.state}}"}
              value={bodyTpl}
              onChange={(e) => setBodyTpl(e.target.value)}
            />
            <span className="muted" style={{ fontSize: 11.5 }}>
              Go templates. Variables:{" "}
              <code>{"{{.rule_name}}"}</code> <code>{"{{.metric}}"}</code>{" "}
              <code>{"{{.value}}"}</code> <code>{"{{.threshold}}"}</code>{" "}
              <code>{"{{.severity}}"}</code> <code>{"{{.state}}"}</code>{" "}
              <code>{"{{.summary}}"}</code>. Leave blank for the default summary.
            </span>
          </div>
        </details>

        {/* name + actions */}
        <div className="m-field">
          <label className="m-field-label">Rule name</label>
          <input
            className="search__input"
            style={{ fontSize: 13 }}
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </div>

        {error && <div className="alert alert--error" style={{ margin: 0 }}>{error}</div>}
        {savedAt > 0 && !error && (
          <div className="m-preview ok" style={{ margin: 0 }}>
            <span className="m-preview-pip" />{" "}
            {healthService ? `Health check added to ${healthService}.` : "Alert rule created."}
          </div>
        )}

        <div className="m-builder-actions">
          <button className="btn btn--primary" type="button" disabled={saving} onClick={create}>
            {saving
              ? healthService
                ? "Adding…"
                : "Creating…"
              : healthService
                ? "Add health check"
                : "Create alert rule"}
          </button>
        </div>
      </div>

      {/* existing rules on this metric */}
      {existing.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <div className="m-section-head">
            <span className="m-section-title">Existing rules on this metric</span>
            <span className="m-section-count">{existing.length}</span>
          </div>
          <div className="m-existing">
            {existing.map((rule) => (
              <div key={rule.id} className="m-existing-row">
                <div className={`m-ex-bar sev-${rule.severity}`} />
                <div className="m-ex-mid">
                  <div className="m-ex-name">{rule.name}</div>
                  <div className="m-ex-cond">
                    {rule.spec.aggregation} {OPS.find((o) => o.op === rule.spec.operator)?.glyph ?? rule.spec.operator}{" "}
                    {rule.spec.threshold} for {rule.spec.for_window}
                    {rule.spec.split_by && <span className="muted"> · split by {rule.spec.split_by}</span>}
                  </div>
                </div>
                <span className={`m-rule-badge sev-${rule.severity}`}>{rule.severity}</span>
                <button className="m-ex-tgt" type="button" onClick={() => removeRule(rule.id)}>
                  remove
                </button>
              </div>
            ))}
          </div>
        </div>
      )}
    </section>
  );
}

function PreviewBanner({
  preview,
  loading,
  op,
  unit,
  splitBy,
}: {
  preview: AlertPreview | null;
  loading: boolean;
  op: string;
  unit?: string;
  splitBy?: string;
}) {
  if (loading && !preview) {
    return <div className="m-preview ok"><span className="m-preview-pip" /> Checking current value…</div>;
  }
  if (!preview) return null;
  const u = unit ? ` ${unit}` : "";
  if (!preview.has_data) {
    return (
      <div className="m-preview ok">
        <span className="m-preview-pip" /> No data for this metric in the window.
      </div>
    );
  }

  // Split-by preview: list which values breach (e.g. which queues), with
  // a count, rather than a single scalar.
  if (splitBy && preview.groups) {
    const th = preview.threshold.toLocaleString(undefined, { maximumFractionDigits: 2 });
    const breaching = preview.groups.filter((g) => g.breached);
    const sorted = [...breaching].sort((a, b) => b.value - a.value).slice(0, 20);
    if (breaching.length === 0) {
      return (
        <div className="m-preview ok">
          <span className="m-preview-pip" />
          <span>
            <b>Quiet.</b> No {splitBy} has {op} <span className="mono">{th}{u}</span> across{" "}
            {preview.groups.length} value{preview.groups.length === 1 ? "" : "s"}.
          </span>
        </div>
      );
    }
    return (
      <div className="m-preview breach">
        <span className="m-preview-pip" />
        <span>
          <b>Would fire for {breaching.length} {splitBy}{breaching.length === 1 ? "" : "s"}</b>{" "}
          (of {preview.groups.length}) {op} <span className="mono">{th}{u}</span>:
          <div style={{ marginTop: 4, display: "flex", flexWrap: "wrap", gap: 4 }}>
            {sorted.map((g) => (
              <span key={g.label} className="badge mono" style={{ fontSize: 11 }}>
                {g.label || "(unset)"}={g.value.toLocaleString(undefined, { maximumFractionDigits: 2 })}
              </span>
            ))}
            {breaching.length > sorted.length && (
              <span className="muted" style={{ fontSize: 11 }}>+{breaching.length - sorted.length} more</span>
            )}
          </div>
        </span>
      </div>
    );
  }

  const val = preview.value.toLocaleString(undefined, { maximumFractionDigits: 2 });
  const th = preview.threshold.toLocaleString(undefined, { maximumFractionDigits: 2 });
  if (preview.breached) {
    return (
      <div className="m-preview breach">
        <span className="m-preview-pip" />
        <span>
          <b>Would fire now.</b> Current <span className="mono">{val}{u}</span> {op} threshold{" "}
          <span className="mono">{th}{u}</span>.
        </span>
      </div>
    );
  }
  return (
    <div className="m-preview ok">
      <span className="m-preview-pip" />
      <span>
        <b>Quiet.</b> Current <span className="mono">{val}{u}</span> is within the threshold.
      </span>
    </div>
  );
}
