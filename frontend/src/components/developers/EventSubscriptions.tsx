// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// EventSubscriptions — the Developers-page manager for outbound event
// subscriptions (issue #4): filter globs over the com.sluicio.* event
// vocabulary, fanned out to a WEBHOOK channel (the channel's payload
// format decides CloudEvents vs canonical JSON). Team-scoped is the
// primary model; org-wide is admin-only.

import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../../api/client";
import type { EventSubscription, EventTypeEntry, Group, NotificationChannel } from "../../api/types";
import { EditDrawer } from "../primitives";

export default function EventSubscriptions() {
  const [subs, setSubs] = useState<EventSubscription[]>([]);
  const [types, setTypes] = useState<EventTypeEntry[]>([]);
  const [groups, setGroups] = useState<Group[]>([]);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<EventSubscription | "new" | null>(null);

  const load = useCallback(() => {
    api.listEventSubscriptions().then((r) => setSubs(r.subscriptions ?? [])).catch((e) => setError(String((e as Error).message ?? e)));
  }, []);
  useEffect(() => {
    load();
    api.listEventTypes().then((r) => setTypes(r.event_types ?? [])).catch(() => {});
    api.listGroups().then((r) => setGroups(r.groups ?? [])).catch(() => {});
    api.listChannels().then((r) => setChannels((r.channels ?? []).filter((c) => c.kind === "webhook"))).catch(() => {});
  }, [load]);

  const groupName = (id?: string) => (id ? groups.find((g) => g.id === id)?.name ?? "team" : "Org-wide");
  const channelName = (id: string) => channels.find((c) => c.id === id)?.name ?? "—";

  const remove = async (s: EventSubscription) => {
    if (!window.confirm(`Delete subscription "${s.name}"? Its destination stops receiving events immediately.`)) return;
    try {
      await api.deleteEventSubscription(s.id);
      load();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  return (
    <div>
      <p className="muted" style={{ fontSize: 13, marginTop: 0 }}>
        Push Sluicio's domain events — alerts firing, errors opening, services discovered, config changes — to your
        platform (Event Grid, EventBridge, n8n, an internal bus) instead of polling. Each subscription filters the{" "}
        <code>com.sluicio.*</code> vocabulary and delivers to a <strong>webhook channel</strong>; the channel's
        payload-format setting picks CloudEvents 1.0 or plain JSON, and HMAC signing applies when configured. Events
        are best-effort notifications — the audit log remains the record.
      </p>
      {error && <div className="alert alert--error">{error}</div>}
      {subs.length > 0 && (
        <table className="table" style={{ maxWidth: 860 }}>
          <thead>
            <tr><th>Name</th><th>Scope</th><th>Filters</th><th>Channel</th><th>Status</th><th></th></tr>
          </thead>
          <tbody>
            {subs.map((s) => (
              <tr key={s.id}>
                <td style={{ fontSize: 13.5, fontWeight: 600 }}>{s.name}</td>
                <td className="muted" style={{ fontSize: 12.5 }}>{groupName(s.group_id)}</td>
                <td>
                  {s.event_filters.map((f) => (
                    <span key={f} className="mono" style={{ fontSize: 11, marginRight: 6, border: "1px solid var(--border)", borderRadius: 4, padding: "1px 5px" }}>{f}</span>
                  ))}
                </td>
                <td className="muted" style={{ fontSize: 12.5 }}>{channelName(s.channel_id)}</td>
                <td className="muted" style={{ fontSize: 12.5 }}>{s.enabled ? "enabled" : "disabled"}</td>
                <td className="num" style={{ whiteSpace: "nowrap" }}>
                  {s.can_manage && (
                    <>
                      <button type="button" className="btn btn--link" onClick={() => setEditing(s)}>Edit</button>
                      <button type="button" className="btn btn--link" onClick={() => remove(s)}>Delete</button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <button type="button" className="btn btn--primary" style={{ marginTop: subs.length ? 10 : 0 }} onClick={() => setEditing("new")}>
        + New subscription
      </button>

      {editing && (
        <SubscriptionDrawer
          existing={editing === "new" ? null : editing}
          types={types}
          groups={groups}
          channels={channels}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            load();
          }}
        />
      )}
    </div>
  );
}

function SubscriptionDrawer({
  existing,
  types,
  groups,
  channels,
  onClose,
  onSaved,
}: {
  existing: EventSubscription | null;
  types: EventTypeEntry[];
  groups: Group[];
  channels: NotificationChannel[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(existing?.name ?? "");
  const [groupID, setGroupID] = useState(existing?.group_id ?? "");
  const [channelID, setChannelID] = useState(existing?.channel_id ?? channels[0]?.id ?? "");
  const [enabled, setEnabled] = useState(existing?.enabled ?? true);
  const [filters, setFilters] = useState<Set<string>>(new Set(existing?.event_filters ?? []));
  const [customFilter, setCustomFilter] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const toggleFilter = (t: string) =>
    setFilters((prev) => {
      const n = new Set(prev);
      n.has(t) ? n.delete(t) : n.add(t);
      return n;
    });

  const customChips = useMemo(
    () => [...filters].filter((f) => !types.some((t) => t.type === f)),
    [filters, types],
  );

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const body = {
        name: name.trim(),
        enabled,
        event_filters: [...filters],
        channel_id: channelID,
      };
      if (existing) {
        await api.updateEventSubscription(existing.id, body);
      } else {
        await api.createEventSubscription({ ...body, group_id: groupID || undefined });
      }
      onSaved();
    } catch (err) {
      setError(String((err as Error).message ?? err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <EditDrawer title={existing ? `Edit "${existing.name}"` : "New event subscription"} width="medium" onClose={onClose}>
      <form className="form" onSubmit={submit} style={{ display: "flex", flexDirection: "column", gap: 12 }}>
        {error && <div className="alert alert--error">{error}</div>}
        <label className="form__label">
          Name
          <input className="search__input" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. platform-bus" required autoFocus />
        </label>
        <div className="form__row">
          <label className="form__label">
            Scope
            {existing ? (
              <input className="search__input" value={existing.group_id ? (groups.find((g) => g.id === existing.group_id)?.name ?? "team") : "Org-wide"} disabled />
            ) : (
              <select className="search__input" value={groupID} onChange={(e) => setGroupID(e.target.value)}>
                <option value="">Org-wide (admin only)</option>
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>Team: {g.name}</option>
                ))}
              </select>
            )}
            <span className="form__hint">
              A team subscription only receives events on entities the team can see. Scope is fixed after create.
            </span>
          </label>
          <label className="form__label">
            Webhook channel
            <select className="search__input" value={channelID} onChange={(e) => setChannelID(e.target.value)} required>
              {channels.length === 0 && <option value="">— create a webhook channel first —</option>}
              {channels.map((c) => (
                <option key={c.id} value={c.id}>{c.name}{c.config?.format === "cloudevents" ? " (CloudEvents)" : ""}</option>
              ))}
            </select>
            <span className="form__hint">
              The channel's payload format + HMAC settings apply to events too.{" "}
              <Link to="/alerts?tab=channels" target="_blank">Manage webhook channels ↗</Link>
            </span>
          </label>
        </div>
        <div>
          <div className="m-field-label" style={{ marginBottom: 6 }}>Event filters</div>
          <div style={{ display: "flex", flexDirection: "column", gap: 4, maxHeight: 240, overflow: "auto", border: "1px solid var(--border)", borderRadius: 6, padding: 8 }}>
            {types.map((t) => (
              <label key={t.type} style={{ display: "flex", gap: 8, alignItems: "baseline", fontSize: 13 }}>
                <input type="checkbox" checked={filters.has(t.type)} onChange={() => toggleFilter(t.type)} />
                <span className="mono" style={{ fontSize: 12 }}>{t.type}</span>
                <span className="muted" style={{ fontSize: 11.5 }}>{t.description}</span>
              </label>
            ))}
          </div>
          <div style={{ display: "flex", gap: 6, marginTop: 6, alignItems: "center", flexWrap: "wrap" }}>
            <input
              className="search__input"
              style={{ maxWidth: 280, fontSize: 12.5 }}
              placeholder="Custom glob, e.g. com.sluicio.ingest_key.*"
              value={customFilter}
              onChange={(e) => setCustomFilter(e.target.value)}
            />
            <button
              type="button"
              className="btn btn--sm"
              disabled={!customFilter.trim()}
              onClick={() => {
                toggleFilter(customFilter.trim());
                setCustomFilter("");
              }}
            >
              Add
            </button>
            <span className="form__hint" style={{ width: "100%" }}>
              Every audited change emits <code>com.sluicio.&lt;entity&gt;.&lt;verb&gt;</code> — more entities than
              this list shows. A pattern ending in <code>*</code> matches everything with that prefix
              (<code>com.sluicio.ingest_key.*</code> = created, revoked, …); an exact type matches only itself.
            </span>
            {customChips.map((f) => (
              <span key={f} className="mono" style={{ fontSize: 11.5, border: "1px solid var(--border)", borderRadius: 4, padding: "2px 6px" }}>
                {f}
                <button type="button" className="btn btn--link" style={{ padding: "0 0 0 4px", fontSize: 12 }} aria-label={`Remove filter ${f}`} onClick={() => toggleFilter(f)}>×</button>
              </span>
            ))}
          </div>
        </div>
        <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13.5 }}>
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          Enabled
        </label>
        <div className="form__actions">
          <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
          <button type="submit" className="btn btn--primary" disabled={busy || filters.size === 0 || !channelID}>
            {busy ? "Saving…" : existing ? "Save" : "Create subscription"}
          </button>
        </div>
      </form>
    </EditDrawer>
  );
}
