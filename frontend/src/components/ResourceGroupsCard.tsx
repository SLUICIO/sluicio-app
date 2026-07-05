// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ResourceGroupsCard — "which groups can view this integration/system"
// (RBAC v2 phase 1). Members of an attached group can see the resource
// and its underlying services (view only). Everyone sees the attached
// list; org admins get a checkbox editor. This is the Community way to
// grant visibility — the richer policy kinds live in Settings → Groups
// (Enterprise).

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { Group, ResourceGroup } from "../api/types";
import { useCurrentUser } from "../lib/useCurrentUser";

export default function ResourceGroupsCard({
  kind,
  id,
}: {
  kind: "integration" | "system";
  id: string;
}) {
  const { can } = useCurrentUser();
  const isAdmin = can("org.manage");
  const [attached, setAttached] = useState<ResourceGroup[] | null>(null);
  const [allGroups, setAllGroups] = useState<Group[] | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const list = kind === "integration" ? api.listIntegrationGroups : api.listSystemGroups;
  const put = kind === "integration" ? api.setIntegrationGroups : api.setSystemGroups;

  useEffect(() => {
    let cancelled = false;
    list(id)
      .then((r) => {
        if (cancelled) return;
        setAttached(r.groups ?? []);
        setSelected(new Set((r.groups ?? []).map((g) => g.group_id)));
      })
      .catch((e) => !cancelled && setError(String((e as Error).message ?? e)));
    if (isAdmin) {
      api
        .listGroups()
        .then((r) => !cancelled && setAllGroups(r.groups ?? []))
        .catch(() => !cancelled && setAllGroups([]));
    }
    return () => {
      cancelled = true;
    };
  }, [id, kind, isAdmin]); // eslint-disable-line react-hooks/exhaustive-deps

  const save = async () => {
    setBusy(true);
    setError(null);
    setSaved(false);
    try {
      await put(id, [...selected]);
      const r = await list(id);
      setAttached(r.groups ?? []);
      setSaved(true);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const dirty =
    attached !== null &&
    (selected.size !== attached.length || attached.some((g) => !selected.has(g.group_id)));

  return (
    <div className="card" style={{ marginBottom: 16, padding: "12px 16px" }}>
      <div style={{ display: "flex", alignItems: "center", gap: 8, marginBottom: 6 }}>
        <span style={{ fontSize: 13, fontWeight: 600 }}>Group access</span>
        {saved && <span className="muted" style={{ fontSize: 12 }}>saved ✓</span>}
      </div>
      <p className="muted" style={{ fontSize: 12, margin: "0 0 10px", lineHeight: 1.5 }}>
        Members of these groups can view this {kind} and its services.
        Non-admins see only what their groups grant. Finer-grained policies
        live in Settings → Groups.
      </p>

      {error && <div className="alert alert--error" style={{ marginBottom: 8 }}>{error}</div>}
      {attached === null ? (
        <div className="placeholder">Loading…</div>
      ) : !isAdmin ? (
        attached.length === 0 ? (
          <span className="muted" style={{ fontSize: 13 }}>No groups attached.</span>
        ) : (
          <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
            {attached.map((g) => (
              <span
                key={g.group_id}
                className="mono"
                style={{
                  fontSize: 12,
                  padding: "3px 8px",
                  borderRadius: 999,
                  border: "1px solid var(--border)",
                  background: "var(--surface-2)",
                }}
              >
                {g.name}
              </span>
            ))}
          </div>
        )
      ) : allGroups === null ? (
        <div className="placeholder">Loading…</div>
      ) : allGroups.length === 0 ? (
        <span className="muted" style={{ fontSize: 13 }}>
          No groups yet — create one in Settings → Groups first.
        </span>
      ) : (
        <>
          <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
            {allGroups.map((g) => (
              <label
                key={g.id}
                style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13, cursor: "pointer" }}
              >
                <input
                  type="checkbox"
                  checked={selected.has(g.id)}
                  onChange={(e) => {
                    const next = new Set(selected);
                    if (e.target.checked) next.add(g.id);
                    else next.delete(g.id);
                    setSelected(next);
                  }}
                />
                <span>{g.name}</span>
                <span className="muted mono" style={{ fontSize: 11 }}>{g.slug}</span>
              </label>
            ))}
          </div>
          <button
            type="button"
            className="btn primary"
            style={{ marginTop: 10 }}
            onClick={save}
            disabled={busy || !dirty}
          >
            {busy ? "Saving…" : "Save group access"}
          </button>
        </>
      )}
    </div>
  );
}
