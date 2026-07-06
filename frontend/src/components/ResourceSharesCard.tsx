// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// ResourceSharesCard — viewer-only shares of one integration/system with
// a user (by email) or a group (RBAC v2 phase 3, Enterprise). Rendered
// only for callers who can manage the resource ("you can share what you
// can manage"); hidden entirely without the rbac_advanced entitlement.

import { FormEvent, useEffect, useState } from "react";
import { api } from "../api/client";
import type { Group, ResourceShare } from "../api/types";
import { useLicense } from "../lib/useLicense";
import { EnterpriseBadge, UpgradeNotice } from "./EnterpriseUpsell";

export default function ResourceSharesCard({
  kind,
  id,
  canManage,
}: {
  kind: "integrations" | "systems";
  id: string;
  canManage: boolean;
}) {
  const { status } = useLicense();
  const entitled = status?.features?.rbac_advanced ?? false;
  const [shares, setShares] = useState<ResourceShare[] | null>(null);
  const [groups, setGroups] = useState<Group[]>([]);
  const [granteeKind, setGranteeKind] = useState<"user" | "group">("user");
  const [email, setEmail] = useState("");
  const [groupId, setGroupId] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const load = () =>
    api
      .listResourceShares(kind, id)
      .then((r) => setShares(r.shares ?? []))
      .catch((e) => setError(String((e as Error).message ?? e)));

  useEffect(() => {
    if (!entitled || !canManage) return;
    let cancelled = false;
    void load().then(() => cancelled);
    api.listGroups().then((r) => !cancelled && setGroups(r.groups ?? [])).catch(() => {});
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind, id, entitled, canManage]);

  // Non-managers never see the sharing surface. A manager on an unlicensed
  // cell sees an upsell rather than a silently-missing card.
  if (!canManage) return null;
  const noun0 = kind === "integrations" ? "integration" : "system";
  if (!entitled) {
    return (
      <div className="card" style={{ marginBottom: 16, padding: "12px 16px" }}>
        <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 6, display: "flex", alignItems: "center", gap: 8 }}>
          Sharing <EnterpriseBadge />
        </div>
        <UpgradeNotice title="Resource sharing is a Sluicio Enterprise feature" expired={status?.expired}>
          <p className="muted" style={{ margin: 0, fontSize: 13 }}>
            An Enterprise license lets you share this {noun0} with a member or
            group as view-only. In the Community edition, use groups + the
            Group access card to grant visibility.
          </p>
        </UpgradeNotice>
      </div>
    );
  }

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.createResourceShare(kind, id, {
        grantee_kind: granteeKind,
        ...(granteeKind === "user" ? { grantee_email: email.trim() } : { grantee_group_id: groupId }),
      });
      setEmail("");
      setGroupId("");
      await load();
    } catch (err) {
      setError(String((err as Error).message ?? err));
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (shareId: string) => {
    try {
      await api.deleteResourceShare(kind, id, shareId);
      await load();
    } catch (err) {
      setError(String((err as Error).message ?? err));
    }
  };

  const noun = kind === "integrations" ? "integration" : "system";
  return (
    <div className="card" style={{ marginBottom: 16, padding: "12px 16px" }}>
      <div style={{ fontSize: 13, fontWeight: 600, marginBottom: 6 }}>Sharing</div>
      <p className="muted" style={{ fontSize: 12, margin: "0 0 10px", lineHeight: 1.5 }}>
        Share this {noun} with a member or group — view only. They'll see it
        and its services' health, traces, logs and metrics; they can't change
        anything. Recipients are notified.
      </p>
      {error && <div className="alert alert--error" style={{ marginBottom: 8 }}>{error}</div>}

      {shares === null ? (
        <div className="placeholder">Loading…</div>
      ) : (
        <>
          {shares.length > 0 && (
            <div style={{ display: "flex", flexDirection: "column", gap: 4, marginBottom: 10 }}>
              {shares.map((sh) => (
                <div key={sh.id} style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13 }}>
                  <span style={{ flex: 1 }}>
                    {sh.grantee_name}
                    <span className="muted" style={{ fontSize: 11, marginLeft: 6 }}>{sh.grantee_kind}</span>
                  </span>
                  <button type="button" className="btn btn--sm" onClick={() => revoke(sh.id)} title="Revoke">
                    Revoke
                  </button>
                </div>
              ))}
            </div>
          )}
          <form onSubmit={submit} style={{ display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
            <select
              className="toolbar__select"
              value={granteeKind}
              onChange={(e) => setGranteeKind(e.target.value as "user" | "group")}
            >
              <option value="user">member</option>
              <option value="group">group</option>
            </select>
            {granteeKind === "user" ? (
              <input
                className="search__input"
                type="email"
                placeholder="member's email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                style={{ width: 220 }}
                required
              />
            ) : (
              <select
                className="toolbar__select"
                value={groupId}
                onChange={(e) => setGroupId(e.target.value)}
                required
              >
                <option value="">Pick a group…</option>
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>{g.name}</option>
                ))}
              </select>
            )}
            <button
              type="submit"
              className="btn primary"
              disabled={busy || (granteeKind === "user" ? !email.trim() : !groupId)}
            >
              {busy ? "Sharing…" : "Share"}
            </button>
          </form>
        </>
      )}
    </div>
  );
}
