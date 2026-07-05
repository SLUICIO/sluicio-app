// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// The Alerts surface, laid out as three columns:
//   1. Health checks — firing now + every configured check (virtualized).
//   2. Notification channels — org-wide channels/profiles, with a note on
//      how teams configure their own under Settings → Teams.
//   3. Sent notifications — delivery history, filtered by the top-bar time
//      window plus service / integration / system / health-check name.
// Rules themselves are created from the Metrics drawer's alert builder;
// this page manages their lifecycle and the channels they route to.

import { useCallback, useEffect, useMemo, useState } from "react";
import type { Dispatch, SetStateAction } from "react";
import { Link } from "react-router-dom";
import { api } from "../api/client";
import type { AlertDelivery, AlertInstance, AlertRule, Group, Integration, NotificationChannel, System } from "../api/types";
import { usePageTitle } from "../lib/usePageTitle";
import { useCurrentUser } from "../lib/useCurrentUser";
import { useAccess } from "../lib/useAccess";
import { useTimeWindow } from "../lib/useTimeWindow";
import { formatRelative } from "../lib/format";
import { alertCondition, alertSignalLabel } from "../lib/alertRule";
import NotificationProfiles from "../components/NotificationProfiles";
import SearchableSelect from "../components/SearchableSelect";
import VirtualInfiniteList from "../components/VirtualInfiniteList";

const KINDS = ["slack", "webhook", "pagerduty", "email"];

export default function Alerts() {
  usePageTitle("Alerts");
  // "Contributor" (editor) and above may manage rules + channels. The
  // cell-api enforces the same floor; this just hides controls viewers
  // can't use.
  const { can } = useCurrentUser();
  // Scoped manage (RBAC v2): channels/profiles are org-global config
  // (org editors only); RULE mutations are per-service — a group-editor
  // may act on rules bound to services in their managed scope.
  const canWrite = can("integration.write");
  const access = useAccess();
  const canManageRule = (serviceName?: string | null) =>
    canWrite || (!!serviceName && access.canManageService(serviceName));
  const [windowVal] = useTimeWindow();

  const [instances, setInstances] = useState<AlertInstance[]>([]);
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [integrations, setIntegrations] = useState<Integration[]>([]);
  const [groups, setGroups] = useState<Group[]>([]);
  const [systems, setSystems] = useState<System[]>([]);
  const [error, setError] = useState<string | null>(null);

  // Sent-notifications: server-side filtered by the window + facets.
  const [deliveries, setDeliveries] = useState<AlertDelivery[]>([]);
  const [dFilter, setDFilter] = useState({ service: "", integration: "", system: "", name: "" });
  const [tab, setTab] = useState<"checks" | "channels" | "sent">("checks");

  const load = useCallback(() => {
    api.listAlertInstances(200).then((r) => setInstances(r.instances ?? [])).catch(() => setInstances([]));
    api.listAlertRules().then((r) => setRules(r.rules ?? [])).catch(() => setRules([]));
    api.listChannels().then((r) => setChannels(r.channels ?? [])).catch(() => setChannels([]));
    api.listIntegrations().then((r) => setIntegrations(r.integrations ?? [])).catch(() => setIntegrations([]));
    api.listGroups().then((r) => setGroups(r.groups ?? [])).catch(() => setGroups([]));
    api.listSystems().then((r) => setSystems(r.systems ?? [])).catch(() => setSystems([]));
  }, []);
  useEffect(() => { load(); }, [load]);

  // Deliveries reload on window / filter change (debounced so typing in the
  // name field doesn't fire a request per keystroke).
  const reloadDeliveries = useCallback(() => {
    api
      .listAlertDeliveries({ range: windowVal, service: dFilter.service, integration: dFilter.integration, system: dFilter.system, name: dFilter.name })
      .then((r) => setDeliveries(r.deliveries ?? []))
      .catch(() => setDeliveries([]));
  }, [windowVal, dFilter]);
  useEffect(() => {
    const t = setTimeout(reloadDeliveries, 250);
    return () => clearTimeout(t);
  }, [reloadDeliveries]);

  const firing = useMemo(() => instances.filter((i) => i.state === "firing"), [instances]);

  const [acting, setActing] = useState<string | null>(null);
  const actOnInstance = useCallback(
    async (id: string, action: "acknowledge" | "resolve") => {
      const prompt =
        action === "acknowledge"
          ? "Acknowledge this alert? It stays open but stops sending notifications while it's being worked on."
          : "Resolve this alert? It closes the alert and won't re-notify while the underlying condition persists.";
      if (!window.confirm(prompt)) return;
      setActing(id);
      try {
        if (action === "acknowledge") await api.acknowledgeAlertInstance(id);
        else await api.resolveAlertInstance(id);
        load();
      } catch (e) {
        setError(String((e as Error).message ?? e));
      } finally {
        setActing(null);
      }
    },
    [load],
  );

  const channelName = useMemo(() => {
    const m = new Map(channels.map((c) => [c.id, c.name]));
    return (id: string) => m.get(id) ?? "—";
  }, [channels]);
  const integrationName = useMemo(() => {
    const m = new Map(integrations.map((i) => [i.id, i.name]));
    return (id: string) => m.get(id) ?? "integration";
  }, [integrations]);
  const groupName = useMemo(() => {
    const m = new Map(groups.map((g) => [g.id, g.name]));
    return (id: string) => m.get(id) ?? "team";
  }, [groups]);
  const systemName = useMemo(() => {
    const m = new Map(systems.map((s) => [s.id, s.name]));
    return (id: string) => m.get(id) ?? "system";
  }, [systems]);

  const toggleRule = async (rule: AlertRule) => {
    try {
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
      load();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  const deleteRule = async (rule: { id: string; name: string }) => {
    if (
      !window.confirm(
        `Delete the health check "${rule.name}"? This removes it everywhere it's bound and stops it from evaluating. This can't be undone.`,
      )
    )
      return;
    try {
      await api.deleteAlertRule(rule.id);
      load();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  // Filter option lists: services + integrations that actually have checks
  // (deliveries only come from rules); systems from the full list.
  const serviceOptions = useMemo(
    () => Array.from(new Set(rules.map((r) => r.service_name).filter((s): s is string => !!s))).sort(),
    [rules],
  );
  const integrationOptions = useMemo(
    () => Array.from(new Set(rules.map((r) => r.integration_id).filter((s): s is string => !!s))),
    [rules],
  );
  const systemOptions = useMemo(() => systems.map((s) => s.id), [systems]);

  const ruleTitle = (rule: AlertRule) => {
    const parts = [alertCondition(rule)];
    parts.push(rule.group_id ? `team: ${groupName(rule.group_id)}` : "org-wide");
    if (rule.channel_ids.length) parts.push(`→ ${rule.channel_ids.map(channelName).join(", ")}`);
    return parts.filter(Boolean).join(" · ");
  };

  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Alerts</h1>
          <p className="page__subtitle">
            Firing alerts, every health check that watches your telemetry, and the channels they notify. Create health checks from a metric in the{" "}
            <Link to="/metrics">Metrics</Link> explorer, from a query on the{" "}
            <Link to="/logs">Logs</Link> page, or from an integration's error breakdown — they're all managed here.
          </p>
        </div>
      </div>

      {error && <div className="alert alert--error">{error}</div>}

      <div className="svc-tabs">
        <button className={`svc-tab ${tab === "checks" ? "on" : ""}`} type="button" onClick={() => setTab("checks")}>
          Health checks
          {firing.length > 0 ? (
            <span className="count" style={{ background: "var(--err)", borderColor: "var(--err)", color: "#fff" }} title={`${firing.length} firing`}>{firing.length}</span>
          ) : rules.length > 0 ? (
            <span className="count">{rules.length}</span>
          ) : null}
        </button>
        <button className={`svc-tab ${tab === "channels" ? "on" : ""}`} type="button" onClick={() => setTab("channels")}>
          Notification channels
          {channels.length > 0 && <span className="count">{channels.length}</span>}
        </button>
        <button className={`svc-tab ${tab === "sent" ? "on" : ""}`} type="button" onClick={() => setTab("sent")}>
          Sent notifications
        </button>
      </div>

      <div style={{ marginTop: 16 }}>
        {tab === "checks" && (
          <HealthChecksColumn
            firing={firing}
            rules={rules}
            canManageRule={canManageRule}
            acting={acting}
            onAct={actOnInstance}
            onToggle={toggleRule}
            onDelete={deleteRule}
            ruleTitle={ruleTitle}
          />
        )}
        {tab === "channels" && <ChannelsCard channels={channels} groups={groups} onChanged={load} onError={setError} canWrite={canWrite} />}
        {tab === "sent" && (
          <SentNotificationsColumn
            deliveries={deliveries}
            filter={dFilter}
            setFilter={setDFilter}
            serviceOptions={serviceOptions}
            integrationOptions={integrationOptions}
            integrationName={integrationName}
            systemOptions={systemOptions}
            systemName={systemName}
          />
        )}
      </div>
    </div>
  );
}

// ── Column 1 ───────────────────────────────────────────────────────────

function HealthChecksColumn({
  firing,
  rules,
  canManageRule,
  acting,
  onAct,
  onToggle,
  onDelete,
  ruleTitle,
}: {
  firing: AlertInstance[];
  rules: AlertRule[];
  canManageRule: (serviceName?: string | null) => boolean;
  acting: string | null;
  onAct: (id: string, action: "acknowledge" | "resolve") => void;
  onToggle: (r: AlertRule) => void;
  onDelete: (r: { id: string; name: string }) => void;
  ruleTitle: (r: AlertRule) => string;
}) {
  const [q, setQ] = useState("");
  // rule id → its firing instance (if any), so a row can badge itself and
  // carry the ack/resolve actions the old "Firing now" section used to hold.
  const firingByRule = useMemo(() => new Map(firing.map((i) => [i.alert_rule_id, i])), [firing]);
  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase();
    const base = needle ? rules.filter((r) => r.name.toLowerCase().includes(needle) || (r.service_name ?? "").toLowerCase().includes(needle)) : rules;
    // Firing checks float to the top so a problem is the first thing you see.
    return [...base].sort((a, b) => (firingByRule.has(b.id) ? 1 : 0) - (firingByRule.has(a.id) ? 1 : 0));
  }, [rules, q, firingByRule]);

  // name | actions — actions is a right-aligned flex cell so a firing row can
  // hold Ack/Resolve ahead of the On/Off toggle and delete without breaking
  // the fixed grid the virtualized list shares across every row.
  const grid = "minmax(0,1fr) auto";

  return (
    <section className="card" style={{ minWidth: 0 }}>
      <div className="card__header">
        Health checks <span className="muted" style={{ fontSize: 12 }}>· {rules.length}</span>
        {firing.length > 0 && <span style={{ fontSize: 12, marginLeft: 6, color: "var(--err)", fontWeight: 600 }}>· {firing.length} firing</span>}
      </div>

      {/* All checks: search + virtualized list. Firing checks are badged and
          sorted to the top — there is no longer a separate section. */}
      <div style={{ padding: "8px 12px 0" }}>
        <input className="search__input" style={{ width: "100%", fontSize: 13 }} placeholder="Search health checks…" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      <VirtualInfiniteList
        items={filtered}
        hasMore={false}
        loadingMore={false}
        loadMore={() => {}}
        gridTemplate={grid}
        height={440}
        rowHeight={40}
        itemKey={(r) => r.id}
        header={<><span>Name</span><span style={{ textAlign: "right" }}>Status</span></>}
        empty={<div className="placeholder" style={{ padding: 12 }}>No health checks. Create one from a metric in the Metrics explorer.</div>}
        renderRow={(rule) => {
          const inst = firingByRule.get(rule.id);
          return (
            <>
              <span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={inst?.summary ?? ruleTitle(rule)}>
                {inst && <span className="m-rule-badge" style={{ marginRight: 6, fontSize: 10, background: "var(--err)", borderColor: "var(--err)", color: "#fff" }}>firing</span>}
                <span className={`m-rule-badge sev-${rule.severity}`} style={{ marginRight: 6, fontSize: 10 }}>{rule.severity}</span>
                {rule.name}
                {alertSignalLabel(rule.signal) && <span className="muted" style={{ fontSize: 11, marginLeft: 6 }}>{alertSignalLabel(rule.signal)}</span>}
                {rule.service_name && <span className="muted" style={{ fontSize: 11, marginLeft: 6 }}>♥ {rule.service_name}</span>}
              </span>
              <span style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: 6 }}>
                {inst &&
                  (inst.handled_at ? (
                    <span className="muted" style={{ fontSize: 11 }}>ack&rsquo;d</span>
                  ) : canManageRule(rule.service_name) ? (
                    <>
                      <button className="btn btn--sm" disabled={acting === inst.id} onClick={() => onAct(inst.id, "acknowledge")} title="Being worked on — silences further notifications">Ack</button>
                      <button className="btn btn--sm" disabled={acting === inst.id} onClick={() => onAct(inst.id, "resolve")}>Resolve</button>
                    </>
                  ) : null)}
                {canManageRule(rule.service_name) ? (
                  <button className="btn btn--sm" type="button" onClick={() => onToggle(rule)} title={rule.enabled ? "Enabled — click to disable" : "Disabled — click to enable"}>
                    {rule.enabled ? "On" : "Off"}
                  </button>
                ) : (
                  <span className="muted" style={{ fontSize: 12 }}>{rule.enabled ? "On" : "Off"}</span>
                )}
                {canManageRule(rule.service_name) && (
                  <button className="btn btn--sm btn--danger" type="button" onClick={() => onDelete(rule)} title="Delete health check">×</button>
                )}
              </span>
            </>
          );
        }}
      />
    </section>
  );
}

// ── Column 3 ───────────────────────────────────────────────────────────

function jobStateBadge(s: string) {
  const cls = s === "succeeded" ? "sev-info" : s === "failed" ? "sev-critical" : "sev-warning";
  return <span className={`m-rule-badge ${cls}`}>{s === "succeeded" ? "sent" : s}</span>;
}

function SentNotificationsColumn({
  deliveries,
  filter,
  setFilter,
  serviceOptions,
  integrationOptions,
  integrationName,
  systemOptions,
  systemName,
}: {
  deliveries: AlertDelivery[];
  filter: { service: string; integration: string; system: string; name: string };
  setFilter: Dispatch<SetStateAction<{ service: string; integration: string; system: string; name: string }>>;
  serviceOptions: string[];
  integrationOptions: string[];
  integrationName: (id: string) => string;
  systemOptions: string[];
  systemName: (id: string) => string;
}) {
  return (
    <section className="card" style={{ minWidth: 0 }}>
      <div className="card__header">Sent notifications <span className="muted" style={{ fontSize: 12 }}>· {deliveries.length} in window</span></div>

      <div style={{ padding: "10px 12px", borderBottom: "1px solid var(--border)" }}>
        <div style={{ display: "flex", gap: 10, alignItems: "center", flexWrap: "wrap" }}>
          <SearchableSelect value={filter.service} onChange={(v) => setFilter((f) => ({ ...f, service: v }))} options={serviceOptions} placeholder="Search services…" allLabel="All services" />
          <SearchableSelect value={filter.integration} onChange={(v) => setFilter((f) => ({ ...f, integration: v }))} options={integrationOptions} labelFor={integrationName} placeholder="Search integrations…" allLabel="All integrations" />
          <SearchableSelect value={filter.system} onChange={(v) => setFilter((f) => ({ ...f, system: v }))} options={systemOptions} labelFor={systemName} placeholder="Search systems…" allLabel="All systems" />
          <div style={{ position: "relative", flex: 1, minWidth: 200, display: "flex", alignItems: "center" }}>
            <span aria-hidden style={{ position: "absolute", left: 10, color: "var(--muted)" }}>⌕</span>
            <input
              className="search__input"
              style={{ paddingLeft: 30, width: "100%", fontSize: 13 }}
              placeholder="Health check name…"
              value={filter.name}
              onChange={(e) => setFilter((f) => ({ ...f, name: e.target.value }))}
            />
          </div>
          {(filter.service || filter.integration || filter.system || filter.name) && (
            <button className="btn btn--link" type="button" onClick={() => setFilter({ service: "", integration: "", system: "", name: "" })}>
              Clear
            </button>
          )}
        </div>
        <span className="muted" style={{ fontSize: 11, marginTop: 6, display: "block" }}>Respects the time window in the top bar.</span>
      </div>

      <VirtualInfiniteList
        items={deliveries}
        hasMore={false}
        loadingMore={false}
        loadMore={() => {}}
        gridTemplate="minmax(0,1fr) auto auto"
        height={440}
        rowHeight={40}
        itemKey={(d) => d.job_id}
        header={<><span>Notification</span><span></span><span style={{ textAlign: "right" }}>when</span></>}
        empty={<div className="placeholder" style={{ padding: 12 }}>Nothing sent in this window. When a check fires and routes to a channel, deliveries show here.</div>}
        renderRow={(d) => (
          <>
            <span
              style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}
              title={`${d.channel_name} (${d.channel_kind}) · ${d.subject || d.summary || ""}${d.job_state === "failed" && d.last_error ? " · ERROR: " + d.last_error : ""}`}
            >
              <span className={`m-rule-badge sev-${d.severity}`} style={{ marginRight: 6, fontSize: 10 }}>{d.alert_state === "resolved" ? "resolved" : "firing"}</span>
              {d.rule_name} <span className="muted" style={{ fontSize: 11 }}>→ {d.channel_name}</span>
            </span>
            {jobStateBadge(d.job_state)}
            <span className="muted" style={{ fontSize: 11, whiteSpace: "nowrap", textAlign: "right" }} title={new Date(d.updated_at).toLocaleString()}>
              {formatRelative(d.updated_at)}
            </span>
          </>
        )}
      />
    </section>
  );
}

// ── Column 2 (channels) ─────────────────────────────────────────────────

// channelDestination renders a one-line summary of where a channel
// delivers, for the table. Secrets (routing keys, passwords) are masked.
function channelDestination(c: NotificationChannel): string {
  switch (c.kind) {
    case "email":
      return c.config.to ? `→ ${c.config.to}` : "—";
    case "pagerduty":
      return c.config.routing_key ? "routing_key ••••" : "—";
    default:
      return c.config.url || "—";
  }
}

// TestChannelButton fires a sample notification to one channel and
// reflects success/failure inline.
function TestChannelButton({ id, onError }: { id: string; onError: (e: string) => void }) {
  const [state, setState] = useState<"idle" | "sending" | "sent">("idle");
  return (
    <button
      className="btn btn--sm"
      type="button"
      disabled={state === "sending"}
      title="Send a sample notification to verify this channel works"
      onClick={async () => {
        setState("sending");
        try {
          await api.testChannel(id);
          setState("sent");
          window.setTimeout(() => setState("idle"), 3000);
        } catch (e) {
          onError(String((e as Error).message ?? e));
          setState("idle");
        }
      }}
    >
      {state === "sending" ? "Sending…" : state === "sent" ? "✓ Sent" : "Send test"}
    </button>
  );
}

function ChannelsCard({
  channels,
  groups,
  onChanged,
  onError,
  canWrite,
}: {
  channels: NotificationChannel[];
  groups: Group[];
  onChanged: () => void;
  onError: (e: string) => void;
  canWrite: boolean;
}) {
  // Team notification channels are managed right here (no longer buried in
  // Settings): pick a team, edit its profiles. Default to the first team so
  // the section shows something the moment there's a team to configure.
  const [team, setTeam] = useState<string>("");
  useEffect(() => {
    if (!team && groups.length > 0) setTeam(groups[0].id);
  }, [groups, team]);
  const teamName = (id: string) => groups.find((g) => g.id === id)?.name ?? id;

  const [name, setName] = useState("");
  const [kind, setKind] = useState("slack");
  const [saving, setSaving] = useState(false);
  const [dest, setDest] = useState("");
  const [email, setEmail] = useState({ smtp_host: "", smtp_port: "587", from: "", to: "", username: "", password: "" });
  const [useSystemEmail, setUseSystemEmail] = useState(true);

  const isEmail = kind === "email";
  const destLabel = kind === "pagerduty" ? "Routing key" : "Webhook URL";
  const destKey = kind === "pagerduty" ? "routing_key" : "url";

  const ready = name.trim().length > 0 && (
    isEmail
      ? email.to.trim().length > 0 && (useSystemEmail || (email.smtp_host.trim() && email.from.trim()))
      : dest.trim().length > 0
  );

  const create = async () => {
    setSaving(true);
    try {
      const config: Record<string, string> = isEmail
        ? useSystemEmail
          ? { to: email.to.trim() }
          : {
              smtp_host: email.smtp_host.trim(),
              smtp_port: email.smtp_port.trim() || "587",
              from: email.from.trim(),
              to: email.to.trim(),
              ...(email.username.trim() ? { username: email.username.trim() } : {}),
              ...(email.password ? { password: email.password } : {}),
            }
        : { [destKey]: dest.trim() };
      await api.createChannel({ name: name.trim(), kind, config });
      setName("");
      setDest("");
      setEmail({ smtp_host: "", smtp_port: "587", from: "", to: "", username: "", password: "" });
      setUseSystemEmail(true);
      onChanged();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (id: string) => {
    if (!window.confirm("Delete this notification channel?")) return;
    try {
      await api.deleteChannel(id);
      onChanged();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    }
  };

  const field = (label: string, value: string, onChange: (v: string) => void, opts?: { placeholder?: string; type?: string; width?: number }) => (
    <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12, minWidth: opts?.width ?? 140 }}>
      <span className="muted">{label}</span>
      <input className="search__input mono" style={{ fontSize: 13 }} type={opts?.type ?? "text"} value={value} onChange={(e) => onChange(e.target.value)} placeholder={opts?.placeholder} />
    </label>
  );

  return (
    <section className="card" style={{ minWidth: 0 }}>
      <div className="card__header">Notification channels <span className="muted" style={{ fontSize: 12 }}>· {channels.length}</span></div>
      <div style={{ padding: "12px 16px", display: "flex", flexDirection: "column", gap: 12 }}>
        {/* Organization channels — the shared delivery-endpoint pool. */}
        <div>
          <div style={{ fontSize: 13, fontWeight: 600 }}>Organization channels</div>
          <p className="muted" style={{ fontSize: 12, margin: "2px 0 0" }}>
            Shared delivery endpoints. The org-wide profile and each team below route alerts to a selection of these.
          </p>
        </div>

        {channels.length > 0 && (
          <table className="table">
            <thead>
              <tr><th>Name</th><th>Kind</th><th>Destination</th>{canWrite && <th></th>}</tr>
            </thead>
            <tbody>
              {channels.map((c) => (
                <tr key={c.id}>
                  <td>{c.name}</td>
                  <td className="mono" style={{ fontSize: 12 }}>{c.kind}</td>
                  <td className="mono muted" style={{ fontSize: 12, maxWidth: 220, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    {channelDestination(c)}
                  </td>
                  {canWrite && (
                    <td>
                      <div style={{ display: "flex", gap: 6 }}>
                        <TestChannelButton id={c.id} onError={onError} />
                        <button className="btn btn--sm btn--danger" type="button" onClick={() => remove(c.id)}>Delete</button>
                      </div>
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        )}

        {canWrite ? (
          <>
            <div style={{ display: "flex", gap: 8, alignItems: "flex-end", flexWrap: "wrap" }}>
              {field("Name", name, setName, { placeholder: "#orders-oncall" })}
              <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12 }}>
                <span className="muted">Kind</span>
                <SearchableSelect value={kind} onChange={(v) => { if (v) setKind(v); }} options={KINDS} placeholder="Search kinds…" allLabel="Select a kind…" />
              </label>
              {isEmail ? (
                <>
                  <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 12, paddingBottom: 8, minWidth: 180 }}>
                    <input type="checkbox" checked={useSystemEmail} onChange={(e) => setUseSystemEmail(e.target.checked)} />
                    Use system email server
                  </label>
                  {field("To", email.to, (v) => setEmail((s) => ({ ...s, to: v })), { placeholder: "oncall@example.com, …", width: 220 })}
                  {!useSystemEmail && (
                    <>
                      {field("SMTP host", email.smtp_host, (v) => setEmail((s) => ({ ...s, smtp_host: v })), { placeholder: "smtp.example.com" })}
                      {field("Port", email.smtp_port, (v) => setEmail((s) => ({ ...s, smtp_port: v })), { placeholder: "587", width: 80 })}
                      {field("From", email.from, (v) => setEmail((s) => ({ ...s, from: v })), { placeholder: "alerts@example.com" })}
                      {field("Username", email.username, (v) => setEmail((s) => ({ ...s, username: v })), { placeholder: "(optional)" })}
                      {field("Password", email.password, (v) => setEmail((s) => ({ ...s, password: v })), { placeholder: "(optional)", type: "password" })}
                    </>
                  )}
                </>
              ) : (
                <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12, flex: 1, minWidth: 220 }}>
                  <span className="muted">{destLabel}</span>
                  <input className="search__input mono" style={{ fontSize: 13 }} value={dest} onChange={(e) => setDest(e.target.value)} placeholder={kind === "pagerduty" ? "R0ABC…" : "https://hooks.slack.com/…"} />
                </label>
              )}
              <button className="btn btn--primary" type="button" disabled={saving || !ready} onClick={create}>
                {saving ? "Adding…" : "Add channel"}
              </button>
            </div>
            <span className="muted" style={{ fontSize: 11.5 }}>
              Slack/webhook take a URL, PagerDuty an Events API routing key. Email sends over SMTP — by default it reuses the org SMTP server from <Link to="/settings?tab=system">Settings → System email</Link>, so you only enter recipients; untick &ldquo;Use system email server&rdquo; to point a channel at its own server.
            </span>
          </>
        ) : (
          <span className="muted" style={{ fontSize: 12 }}>You need contributor access to add or remove notification channels.</span>
        )}

        {/* Organization notification profiles — the org-wide fallback. */}
        {channels.length > 0 && (
          <div style={{ borderTop: "1px solid var(--border)", paddingTop: 12, display: "flex", flexDirection: "column", gap: 6 }}>
            <div style={{ fontSize: 13, fontWeight: 600 }}>Organization notification profiles</div>
            <p className="muted" style={{ fontSize: 12, margin: 0 }}>
              A profile bundles delivery behaviour (grouping + re-notify interval) with channels. The default org-wide profile is the final fallback when an alert has no team- or integration-specific profile.
            </p>
            <NotificationProfiles groupId={null} channels={channels} canWrite={canWrite} />
          </div>
        )}

        {/* Team notification channels — managed here, not in Settings. Pick a
            team and edit its own profiles; they take priority over the org
            fallback when a health check that belongs to the team fires. */}
        <div style={{ borderTop: "1px solid var(--border)", paddingTop: 12, display: "flex", flexDirection: "column", gap: 8 }}>
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "flex-end", gap: 12, flexWrap: "wrap" }}>
            <div>
              <div style={{ fontSize: 13, fontWeight: 600 }}>Team notification channels</div>
              <p className="muted" style={{ fontSize: 12, margin: "2px 0 0" }}>
                A team&rsquo;s own profiles take over when one of its health checks fires — Sluicio notifies the team&rsquo;s channels and only falls back to the org-wide default if the team hasn&rsquo;t set any.
              </p>
            </div>
            {groups.length > 0 && (
              <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12, minWidth: 200 }}>
                <span className="muted">Team</span>
                <SearchableSelect value={team} onChange={(v) => setTeam(v)} options={groups.map((g) => g.id)} labelFor={teamName} placeholder="Search teams…" allLabel="Select a team…" />
              </label>
            )}
          </div>

          {groups.length === 0 ? (
            <p className="muted" style={{ fontSize: 12, margin: 0 }}>
              No teams yet. Create one under <Link to="/settings?tab=groups">Settings → Groups</Link> to give it its own channels.
            </p>
          ) : !team ? (
            <p className="muted" style={{ fontSize: 12, margin: 0 }}>Pick a team above to view and manage its notification channels.</p>
          ) : (
            <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
              <div style={{ fontSize: 12 }}>These are <strong>{teamName(team)}</strong>&rsquo;s notification channels.</div>
              <NotificationProfiles groupId={team} channels={channels} canWrite={canWrite} />
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
