// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// NotificationProfiles — manage the notification profiles for one scope:
// a team (groupId set) or the org-wide fallback (groupId null). A profile
// bundles delivery behaviour (grouping + re-notify interval) with a set of
// channels. When an alert or unacknowledged error fires it resolves to one
// profile — the integration's assigned profile, else the owning team's
// default, else the org-wide default — and delivers to that profile's
// channels. The same control drives the team scope (Settings → Groups) and
// the org scope (Alerts page).

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type {
  NotificationChannel,
  NotificationProfile,
  ProfileEnvironmentMode,
  ProfileGrouping,
} from "../api/types";

type Draft = {
  name: string;
  grouping: ProfileGrouping;
  renotify_minutes: number;
  is_default: boolean;
  channel_ids: string[];
  environment_mode: ProfileEnvironmentMode;
  environments: string[];
};

function toDraft(p: NotificationProfile): Draft {
  return {
    name: p.name,
    grouping: p.grouping,
    renotify_minutes: p.renotify_minutes,
    is_default: p.is_default,
    channel_ids: [...p.channel_ids],
    environment_mode: p.environment_mode ?? "any",
    environments: [...(p.environments ?? [])],
  };
}

function draftsEqual(a: Draft, b: Draft): boolean {
  return (
    a.name === b.name &&
    a.grouping === b.grouping &&
    a.renotify_minutes === b.renotify_minutes &&
    a.is_default === b.is_default &&
    a.environment_mode === b.environment_mode &&
    a.environments.join("\u0000") === b.environments.join("\u0000") &&
    a.channel_ids.length === b.channel_ids.length &&
    a.channel_ids.every((id) => b.channel_ids.includes(id))
  );
}

export default function NotificationProfiles({
  groupId,
  channels,
  canWrite,
}: {
  groupId: string | null;
  channels: NotificationChannel[];
  canWrite: boolean;
}) {
  const [profiles, setProfiles] = useState<NotificationProfile[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const scopeKey = groupId ?? "__org__";

  const reload = () =>
    api
      .listNotificationProfiles()
      .then((r) =>
        setProfiles(
          (r.profiles ?? []).filter((p) => (p.group_id ?? null) === groupId),
        ),
      )
      .catch((e) => setError(String((e as Error).message ?? e)))
      .finally(() => setLoaded(true));

  useEffect(() => {
    let cancelled = false;
    setLoaded(false);
    api
      .listNotificationProfiles()
      .then((r) => {
        if (cancelled) return;
        setProfiles(
          (r.profiles ?? []).filter((p) => (p.group_id ?? null) === groupId),
        );
      })
      .catch((e) => {
        if (!cancelled) setError(String((e as Error).message ?? e));
      })
      .finally(() => {
        if (!cancelled) setLoaded(true);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scopeKey]);

  const addProfile = async () => {
    setError(null);
    try {
      await api.createNotificationProfile({
        group_id: groupId,
        name: profiles.length === 0 ? "Default" : "New profile",
        grouping: "per_check",
        renotify_minutes: 0,
        is_default: profiles.length === 0,
        channel_ids: [],
      });
      await reload();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  if (!loaded) {
    return <p className="muted" style={{ fontSize: 12, margin: 0 }}>Loading profiles…</p>;
  }

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
      {error && (
        <div className="alert alert--error" style={{ margin: 0 }}>{error}</div>
      )}
      {profiles.length === 0 && (
        <p className="muted" style={{ fontSize: 12, margin: 0 }}>
          No profiles yet. Add one to control how notifications are delivered for this {groupId ? "team" : "organization"}.
        </p>
      )}
      {profiles.map((p) => (
        <ProfileCard
          key={p.id}
          profile={p}
          channels={channels}
          canWrite={canWrite}
          onChanged={reload}
          onError={setError}
        />
      ))}
      {canWrite && (
        <div>
          <button className="btn btn--sm" onClick={addProfile}>
            + Add profile
          </button>
        </div>
      )}
    </div>
  );
}

function ProfileCard({
  profile,
  channels,
  canWrite,
  onChanged,
  onError,
}: {
  profile: NotificationProfile;
  channels: NotificationChannel[];
  canWrite: boolean;
  onChanged: () => Promise<void> | void;
  onError: (msg: string) => void;
}) {
  const [draft, setDraft] = useState<Draft>(toDraft(profile));
  // The typed text, kept apart from the parsed list so a comma being
  // typed is not normalised away mid-word.
  const [envText, setEnvText] = useState(() => (profile.environments ?? []).join(", "));
  const [saving, setSaving] = useState(false);

  // Re-sync when the underlying profile changes (e.g. after a sibling save
  // flips the default).
  useEffect(() => {
    setDraft(toDraft(profile));
  }, [profile]);

  const dirty = !draftsEqual(draft, toDraft(profile));

  const toggleChannel = (id: string) => {
    setDraft((d) => ({
      ...d,
      channel_ids: d.channel_ids.includes(id)
        ? d.channel_ids.filter((c) => c !== id)
        : [...d.channel_ids, id],
    }));
  };

  const save = async () => {
    setSaving(true);
    try {
      await api.updateNotificationProfile(profile.id, {
        name: draft.name.trim() || "Untitled",
        grouping: draft.grouping,
        renotify_minutes: Math.max(0, Math.floor(draft.renotify_minutes || 0)),
        is_default: draft.is_default,
        environment_mode: draft.environment_mode,
        environments: draft.environment_mode === "any" ? [] : draft.environments,
      });
      await api.setNotificationProfileChannels(profile.id, draft.channel_ids);
      await onChanged();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  const remove = async () => {
    if (!window.confirm(`Delete notification profile "${profile.name}"?`)) return;
    setSaving(true);
    try {
      await api.deleteNotificationProfile(profile.id);
      await onChanged();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  const field: React.CSSProperties = { display: "flex", flexDirection: "column", gap: 4 };
  const labelStyle: React.CSSProperties = { fontSize: 11, fontWeight: 600, color: "var(--text-muted)" };

  return (
    <div
      style={{
        border: "1px solid var(--border)",
        borderRadius: 8,
        padding: 12,
        display: "flex",
        flexDirection: "column",
        gap: 10,
        background: "var(--surface-2, transparent)",
      }}
    >
      <div style={{ display: "flex", gap: 12, flexWrap: "wrap", alignItems: "flex-end" }}>
        <label style={{ ...field, flex: "1 1 180px" }}>
          <span style={labelStyle}>Name</span>
          <input
            className="search__input"
            style={{ fontSize: 13 }}
            value={draft.name}
            disabled={!canWrite}
            onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))}
          />
        </label>
        <label style={field}>
          <span style={labelStyle}>Group alerts</span>
          <select
            className="search__input"
            style={{ fontSize: 13 }}
            value={draft.grouping}
            disabled={!canWrite}
            onChange={(e) =>
              setDraft((d) => ({ ...d, grouping: e.target.value as ProfileGrouping }))
            }
          >
            <option value="per_check">One per health check</option>
            <option value="per_integration">One per integration</option>
          </select>
        </label>
        <label style={field}>
          <span style={labelStyle}>Re-notify every (min)</span>
          <input
            className="search__input"
            style={{ fontSize: 13, width: 110 }}
            type="number"
            min={0}
            value={draft.renotify_minutes}
            disabled={!canWrite}
            onChange={(e) =>
              setDraft((d) => ({ ...d, renotify_minutes: Number(e.target.value) }))
            }
          />
        </label>
        <label
          style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 13, paddingBottom: 6 }}
        >
          <input
            type="checkbox"
            checked={draft.is_default}
            disabled={!canWrite}
            onChange={(e) => setDraft((d) => ({ ...d, is_default: e.target.checked }))}
          />
          Default for this scope
        </label>
      </div>

      {/* Routing by environment (issue #40): "production pages, test
          posts to Slack" is a production-only profile on the
          integration over a default for the rest. */}
      <div style={{ display: "flex", gap: 12, flexWrap: "wrap", alignItems: "flex-end" }}>
        <label style={field}>
          <span style={labelStyle}>Environments</span>
          <select
            className="search__input"
            style={{ fontSize: 13 }}
            value={draft.environment_mode}
            disabled={!canWrite}
            onChange={(e) => setDraft((d) => ({ ...d, environment_mode: e.target.value as ProfileEnvironmentMode }))}
          >
            <option value="any">Every environment</option>
            <option value="only">Only these</option>
            <option value="except">All except these</option>
          </select>
        </label>
        {draft.environment_mode !== "any" && (
          <label style={{ ...field, flex: "1 1 220px" }}>
            <span style={labelStyle}>Environment names, comma separated</span>
            <input
              className="search__input"
              style={{ fontSize: 13 }}
              disabled={!canWrite}
              placeholder="production"
              value={envText}
              onChange={(e) => {
                setEnvText(e.target.value);
                const environments = e.target.value.split(",").map((x) => x.trim()).filter(Boolean);
                setDraft((d) => ({ ...d, environments }));
              }}
            />
          </label>
        )}
      </div>
      {draft.environment_mode !== "any" && (
        <p className="muted" style={{ fontSize: 11.5, margin: 0 }}>
          An alert about another environment passes this profile over for the next one: the team default,
          then the org default. Alerts that are not about an environment use this profile as before.
        </p>
      )}

      <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
        <span style={labelStyle}>Channels</span>
        {channels.length === 0 ? (
          <p className="muted" style={{ fontSize: 12, margin: 0 }}>
            No channels configured — add one on the Alerts page first.
          </p>
        ) : (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 10 }}>
            {channels.map((c) => (
              <label
                key={c.id}
                style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 13, cursor: canWrite ? "pointer" : "default", opacity: canWrite ? 1 : 0.6 }}
              >
                <input
                  type="checkbox"
                  disabled={!canWrite}
                  checked={draft.channel_ids.includes(c.id)}
                  onChange={() => toggleChannel(c.id)}
                />
                {c.name} <span className="muted">· {c.kind}</span>
              </label>
            ))}
          </div>
        )}
      </div>

      {canWrite && (
        <div style={{ display: "flex", gap: 8 }}>
          <button
            className="btn btn--sm btn--primary"
            disabled={!dirty || saving}
            onClick={save}
          >
            {saving ? "Saving…" : "Save"}
          </button>
          <button className="btn btn--sm btn--danger" disabled={saving} onClick={remove}>
            Delete
          </button>
        </div>
      )}
    </div>
  );
}
