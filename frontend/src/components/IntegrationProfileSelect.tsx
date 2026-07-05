// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// IntegrationProfileSelect — assign a notification profile to one
// integration, overriding the default resolution. Leaving it on "Inherit"
// falls back to the owning team's default profile, then the org-wide
// default. Profiles themselves (channels + behaviour) are managed per-team
// in Settings → Groups and org-wide on the Alerts page.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { NotificationProfile } from "../api/types";

export default function IntegrationProfileSelect({
  integrationId,
  canWrite,
}: {
  integrationId: string;
  canWrite: boolean;
}) {
  const [profiles, setProfiles] = useState<NotificationProfile[]>([]);
  const [groupNames, setGroupNames] = useState<Record<string, string>>({});
  const [assigned, setAssigned] = useState<string>(""); // "" = inherit
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    Promise.all([
      api.listNotificationProfiles(),
      api.getIntegrationProfile(integrationId),
      api.listGroups().catch(() => ({ groups: [] })),
    ])
      .then(([pr, cur, gr]) => {
        if (cancelled) return;
        setProfiles(pr.profiles ?? []);
        setAssigned(cur.profile_id ?? "");
        const names: Record<string, string> = {};
        for (const g of gr.groups ?? []) names[g.id] = g.name;
        setGroupNames(names);
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
  }, [integrationId]);

  const onChange = async (value: string) => {
    setAssigned(value);
    setSaving(true);
    setError(null);
    try {
      await api.assignIntegrationProfile(integrationId, value || null);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  if (!loaded) {
    return <p className="muted" style={{ fontSize: 12, margin: 0 }}>Loading…</p>;
  }

  const scopeLabel = (p: NotificationProfile) =>
    p.group_id ? groupNames[p.group_id] ?? "Team" : "Org-wide";

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
      {error && <div className="alert alert--error" style={{ margin: 0 }}>{error}</div>}
      <select
        className="search__input"
        style={{ fontSize: 13, maxWidth: 360 }}
        value={assigned}
        disabled={!canWrite || saving}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">Inherit (team or org default)</option>
        {profiles.map((p) => (
          <option key={p.id} value={p.id}>
            {p.name} — {scopeLabel(p)}
            {p.is_default ? " (default)" : ""}
          </option>
        ))}
      </select>
    </div>
  );
}
