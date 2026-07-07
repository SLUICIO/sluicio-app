// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Maintenance windows — the "Maintenance" tab on the Alerts page. While
// a window is active, alert delivery for its scope is silenced (the
// engine keeps evaluating and recording; see
// docs/maintenance-and-announcements-design.md). Editors schedule
// windows for entities/team scopes; org-wide silence needs an admin.

import { FormEvent, useMemo, useState } from "react";
import { api } from "../api/client";
import type { Group, Integration, MaintenanceWindow, MaintenanceWindowScope, System } from "../api/types";
import { formatRelative } from "../lib/format";
import { announcementsChanged } from "./AnnouncementsBanner";
import SearchableSelect from "./SearchableSelect";

interface Props {
  windows: MaintenanceWindow[];
  integrations: Integration[];
  systems: System[];
  groups: Group[];
  canWrite: boolean;
  isAdmin: boolean;
  onChanged: () => void;
  onError: (msg: string) => void;
}

const DURATIONS: { label: string; hours: number }[] = [
  { label: "1 hour", hours: 1 },
  { label: "4 hours", hours: 4 },
  { label: "8 hours", hours: 8 },
  { label: "24 hours", hours: 24 },
];

export default function MaintenanceWindows({
  windows, integrations, systems, groups, canWrite, isAdmin, onChanged, onError,
}: Props) {
  const [creating, setCreating] = useState(false);

  const integrationName = useMemo(() => {
    const m = new Map(integrations.map((i) => [i.id, i.name]));
    return (id: string) => m.get(id) ?? "integration";
  }, [integrations]);
  const systemName = useMemo(() => {
    const m = new Map(systems.map((s) => [s.id, s.name]));
    return (id: string) => m.get(id) ?? "system";
  }, [systems]);
  const groupName = useMemo(() => {
    const m = new Map(groups.map((g) => [g.id, g.name]));
    return (id: string) => m.get(id) ?? "team";
  }, [groups]);

  const scopeSummary = (scope: MaintenanceWindowScope) => {
    switch (scope.kind) {
      case "all_org":
        return "Whole organization";
      case "group":
        return `Team: ${scope.group_id ? groupName(scope.group_id) : "—"}`;
      case "entities": {
        const parts: string[] = [];
        for (const id of scope.integration_ids ?? []) parts.push(integrationName(id));
        for (const id of scope.system_ids ?? []) parts.push(systemName(id));
        for (const n of scope.service_names ?? []) parts.push(n);
        return parts.join(", ") || "—";
      }
      default:
        return "—";
    }
  };

  const endWindow = async (w: MaintenanceWindow) => {
    const verb = w.active ? "End" : "Cancel";
    if (!window.confirm(`${verb} "${w.name}"? Alert delivery for its scope resumes immediately.`)) return;
    try {
      await api.endMaintenanceWindow(w.id);
      announcementsChanged(); // linked banner goes with the window
      onChanged();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    }
  };

  const upcoming = windows.filter((w) => w.active || new Date(w.starts_at) > new Date());
  const past = windows.filter((w) => !w.active && new Date(w.starts_at) <= new Date());

  return (
    <div style={{ maxWidth: 760 }}>
      <p className="muted" style={{ fontSize: 13, lineHeight: 1.55, margin: "0 0 14px" }}>
        During a maintenance window, alerts in its scope keep evaluating and
        recording but stay <strong>silent</strong> — no emails, webhooks, or
        notification pings. Anything still firing when the window ends
        notifies then. Windows last at most 7 days.
      </p>

      {canWrite && !creating && (
        <button type="button" className="btn btn--primary" onClick={() => setCreating(true)} style={{ marginBottom: 16 }}>
          Schedule maintenance
        </button>
      )}
      {creating && (
        <WindowForm
          integrations={integrations}
          systems={systems}
          groups={groups}
          isAdmin={isAdmin}
          onDone={() => { setCreating(false); onChanged(); }}
          onCancel={() => setCreating(false)}
          onError={onError}
        />
      )}

      {upcoming.length === 0 && past.length === 0 && (
        <p className="muted" style={{ fontSize: 13 }}>No maintenance windows yet.</p>
      )}

      {upcoming.length > 0 && (
        <table className="table" style={{ marginBottom: 20 }}>
          <thead>
            <tr><th>Window</th><th>Scope</th><th>When</th><th>Status</th><th></th></tr>
          </thead>
          <tbody>
            {upcoming.map((w) => (
              <tr key={w.id}>
                <td>
                  <div className="font-medium">{w.name}</div>
                  {w.reason && <div className="muted" style={{ fontSize: 12 }}>{w.reason}</div>}
                </td>
                <td style={{ fontSize: 13 }}>{scopeSummary(w.scope)}</td>
                <td style={{ fontSize: 13 }} title={`${new Date(w.starts_at).toLocaleString()} → ${new Date(w.ends_at).toLocaleString()}`}>
                  {w.active
                    ? `until ${new Date(w.ends_at).toLocaleString()}`
                    : `${new Date(w.starts_at).toLocaleString()} → ${new Date(w.ends_at).toLocaleString()}`}
                </td>
                <td>
                  <span
                    className="mono"
                    style={{
                      fontSize: 11, fontWeight: 700, padding: "2px 8px", borderRadius: 999,
                      color: w.active ? "var(--warn-ink, #92400e)" : "var(--muted)",
                      background: w.active ? "var(--warn-soft, #fef3c7)" : "transparent",
                      border: w.active ? "1px solid transparent" : "1px solid var(--border)",
                    }}
                  >
                    {w.active ? "ACTIVE" : "SCHEDULED"}
                  </span>
                </td>
                <td className="num">
                  {canWrite && (
                    <button type="button" className="btn btn--link" onClick={() => endWindow(w)}>
                      {w.active ? "End now" : "Cancel"}
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {past.length > 0 && (
        <>
          <h3 style={{ fontSize: 13, fontWeight: 600, margin: "0 0 8px" }}>Recent windows</h3>
          <table className="table">
            <thead>
              <tr><th>Window</th><th>Scope</th><th>Ended</th></tr>
            </thead>
            <tbody>
              {past.map((w) => (
                <tr key={w.id}>
                  <td className="muted">{w.name}</td>
                  <td className="muted" style={{ fontSize: 13 }}>{scopeSummary(w.scope)}</td>
                  <td className="muted" style={{ fontSize: 13 }}>{formatRelative(w.ends_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </div>
  );
}

// ── create form ──────────────────────────────────────────────────────

function WindowForm({
  integrations, systems, groups, isAdmin, onDone, onCancel, onError,
}: {
  integrations: Integration[];
  systems: System[];
  groups: Group[];
  isAdmin: boolean;
  onDone: () => void;
  onCancel: () => void;
  onError: (msg: string) => void;
}) {
  const [name, setName] = useState("");
  const [reason, setReason] = useState("");
  const [kind, setKind] = useState<MaintenanceWindowScope["kind"]>("entities");
  const [groupID, setGroupID] = useState("");
  const [integrationIDs, setIntegrationIDs] = useState<string[]>([]);
  const [systemIDs, setSystemIDs] = useState<string[]>([]);
  const [durationH, setDurationH] = useState(4);
  const [customEnd, setCustomEnd] = useState(""); // datetime-local, "" = use duration
  const [announce, setAnnounce] = useState(false);
  const [busy, setBusy] = useState(false);

  const integrationName = (id: string) => integrations.find((i) => i.id === id)?.name ?? id;
  const systemName = (id: string) => systems.find((s) => s.id === id)?.name ?? id;
  const groupLabel = (id: string) => groups.find((g) => g.id === id)?.name ?? id;

  const pickKind = (k: MaintenanceWindowScope["kind"]) => {
    setKind(k);
    // Q2 from the design review: announce defaults on for org-wide
    // silence, off otherwise.
    setAnnounce(k === "all_org");
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (busy) return;
    const scope: MaintenanceWindowScope =
      kind === "all_org" ? { kind } :
      kind === "group" ? { kind, group_id: groupID } :
      { kind, integration_ids: integrationIDs, system_ids: systemIDs };
    const ends = customEnd
      ? new Date(customEnd)
      : new Date(Date.now() + durationH * 3600_000);
    setBusy(true);
    try {
      await api.createMaintenanceWindow({
        name: name.trim(),
        reason: reason.trim(),
        ends_at: ends.toISOString(),
        scope,
        announce,
      });
      if (announce) announcementsChanged();
      onDone();
    } catch (err) {
      onError(String((err as Error).message ?? err));
      setBusy(false);
    }
  };

  const scopeReady =
    kind === "all_org" ||
    (kind === "group" && !!groupID) ||
    (kind === "entities" && (integrationIDs.length > 0 || systemIDs.length > 0));

  return (
    <form
      onSubmit={submit}
      className="card"
      style={{ padding: 16, marginBottom: 20, display: "flex", flexDirection: "column", gap: 12 }}
    >
      <div style={{ display: "flex", gap: 12 }}>
        <label className="form__label" style={{ flex: 1 }}>
          Name
          <input className="search__input" value={name} onChange={(e) => setName(e.target.value)}
            placeholder="July release" autoFocus required />
        </label>
        <label className="form__label" style={{ flex: 2 }}>
          Reason <span className="muted">(optional, shown on the banner)</span>
          <input className="search__input" value={reason} onChange={(e) => setReason(e.target.value)}
            placeholder="Upgrading the ERP connector" />
        </label>
      </div>

      <div>
        <div className="form__label" style={{ marginBottom: 6 }}>Silence alerts for</div>
        <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
          <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13.5 }}>
            <input type="radio" name="mw-scope" checked={kind === "entities"} onChange={() => pickKind("entities")} />
            Selected integrations &amp; systems
          </label>
          <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13.5 }}>
            <input type="radio" name="mw-scope" checked={kind === "group"} onChange={() => pickKind("group")} />
            A team's health checks
          </label>
          <label
            style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13.5, opacity: isAdmin ? 1 : 0.5 }}
            title={isAdmin ? undefined : "Org-wide silence requires an org admin"}
          >
            <input type="radio" name="mw-scope" disabled={!isAdmin} checked={kind === "all_org"} onChange={() => pickKind("all_org")} />
            The whole organization
          </label>
        </div>
      </div>

      {kind === "entities" && (
        <div style={{ display: "flex", gap: 12, flexWrap: "wrap" }}>
          <div>
            <div className="form__label" style={{ marginBottom: 4 }}>Integrations</div>
            <SearchableSelect
              value=""
              allLabel="Add integration…"
              options={integrations.map((i) => i.id).filter((id) => !integrationIDs.includes(id))}
              labelFor={integrationName}
              onChange={(id) => id && setIntegrationIDs((cur) => [...cur, id])}
            />
            <Chips values={integrationIDs} labelFor={integrationName}
              onRemove={(id) => setIntegrationIDs((cur) => cur.filter((x) => x !== id))} />
          </div>
          <div>
            <div className="form__label" style={{ marginBottom: 4 }}>Systems</div>
            <SearchableSelect
              value=""
              allLabel="Add system…"
              options={systems.map((s) => s.id).filter((id) => !systemIDs.includes(id))}
              labelFor={systemName}
              onChange={(id) => id && setSystemIDs((cur) => [...cur, id])}
            />
            <Chips values={systemIDs} labelFor={systemName}
              onRemove={(id) => setSystemIDs((cur) => cur.filter((x) => x !== id))} />
          </div>
        </div>
      )}

      {kind === "group" && (
        <div>
          <div className="form__label" style={{ marginBottom: 4 }}>Team</div>
          <SearchableSelect
            value={groupID}
            allLabel="Choose a team…"
            options={groups.map((g) => g.id)}
            labelFor={groupLabel}
            onChange={setGroupID}
          />
          <p className="muted" style={{ fontSize: 12, margin: "6px 0 0" }}>
            Silences the health checks this team owns. Org-wide checks that
            happen to cover the team's services keep alerting.
          </p>
        </div>
      )}

      <div style={{ display: "flex", gap: 12, alignItems: "flex-end", flexWrap: "wrap" }}>
        <label className="form__label">
          Duration
          <select
            className="search__input"
            value={customEnd ? "custom" : String(durationH)}
            onChange={(e) => {
              if (e.target.value === "custom") {
                const d = new Date(Date.now() + 4 * 3600_000);
                d.setMinutes(0, 0, 0);
                setCustomEnd(d.toISOString().slice(0, 16));
              } else {
                setCustomEnd("");
                setDurationH(Number(e.target.value));
              }
            }}
          >
            {DURATIONS.map((d) => (
              <option key={d.hours} value={d.hours}>{d.label}</option>
            ))}
            <option value="custom">Until a specific time…</option>
          </select>
        </label>
        {customEnd && (
          <label className="form__label">
            Ends at
            <input type="datetime-local" className="search__input" value={customEnd}
              onChange={(e) => setCustomEnd(e.target.value)} required />
          </label>
        )}
        <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13.5, paddingBottom: 8 }}>
          <input type="checkbox" checked={announce} onChange={(e) => setAnnounce(e.target.checked)} />
          Show a banner to all users while active
        </label>
      </div>

      <div style={{ display: "flex", gap: 8 }}>
        <button type="submit" className="btn btn--primary" disabled={busy || !name.trim() || !scopeReady}>
          {busy ? "Scheduling…" : "Start maintenance"}
        </button>
        <button type="button" className="btn" onClick={onCancel}>Cancel</button>
      </div>
    </form>
  );
}

function Chips({ values, labelFor, onRemove }: {
  values: string[];
  labelFor: (v: string) => string;
  onRemove: (v: string) => void;
}) {
  if (values.length === 0) return null;
  return (
    <div style={{ display: "flex", flexWrap: "wrap", gap: 6, marginTop: 6, maxWidth: 320 }}>
      {values.map((v) => (
        <span
          key={v}
          className="mono"
          style={{
            display: "inline-flex", alignItems: "center", gap: 4, fontSize: 12,
            padding: "2px 8px", borderRadius: 999,
            background: "var(--surface-3)", border: "1px solid var(--border)",
          }}
        >
          {labelFor(v)}
          <button type="button" aria-label={`Remove ${labelFor(v)}`} onClick={() => onRemove(v)}
            style={{ background: "none", border: 0, cursor: "pointer", color: "var(--muted)", padding: 0 }}>
            ×
          </button>
        </span>
      ))}
    </div>
  );
}
