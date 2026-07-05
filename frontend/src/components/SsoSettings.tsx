// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Settings → SSO management UI (EE): configure OIDC providers + claim→role/team
// mappings. Rendered by Settings' SsoTab only when the sso entitlement is on.
// See docs/sso.md.

import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { AuthProvider, ClaimMapping, Group, OrgRole } from "../api/types";

const ROLES: OrgRole[] = ["admin", "editor", "viewer"];

type Draft = Partial<AuthProvider> & { client_secret?: string };

const blankDraft = (): Draft => ({
  name: "",
  issuer_url: "",
  client_id: "",
  client_secret: "",
  scopes: "openid email profile",
  claim_groups: "groups",
  claim_email: "email",
  claim_name: "name",
  claim_sub: "sub",
  default_role: "viewer",
  jit_provisioning: true,
  enabled: true,
});

const errMsg = (e: unknown): string =>
  e && typeof e === "object" && "message" in e ? String((e as { message: unknown }).message) : String(e);

export default function SsoSettings() {
  const [providers, setProviders] = useState<AuthProvider[] | null>(null);
  const [groups, setGroups] = useState<Group[]>([]);
  const [draft, setDraft] = useState<Draft | null>(null);
  const [error, setError] = useState<string | null>(null);

  const callbackURL = `${window.location.origin}/api/v1/auth/sso/callback`;

  const refresh = () => {
    api.listAuthProviders().then((r) => setProviders(r.providers)).catch((e) => setError(errMsg(e)));
    api.listGroups().then((r) => setGroups(r.groups ?? [])).catch(() => {});
  };
  useEffect(refresh, []);

  const save = async () => {
    if (!draft) return;
    setError(null);
    try {
      if (draft.id) await api.updateAuthProvider(draft.id, draft);
      else await api.createAuthProvider(draft);
      setDraft(null);
      refresh();
    } catch (e) {
      setError(errMsg(e));
    }
  };

  const remove = async (p: AuthProvider) => {
    if (!window.confirm(`Delete "${p.name}"? Users who sign in only via this provider will lose access.`)) return;
    try {
      await api.deleteAuthProvider(p.id);
      refresh();
    } catch (e) {
      setError(errMsg(e));
    }
  };

  return (
    <div style={{ maxWidth: 760 }}>
      {error && <div className="alert alert--error" role="alert" style={{ marginBottom: 12 }}>{error}</div>}

      <div className="muted" style={{ fontSize: 13, lineHeight: 1.6, marginBottom: 14 }}>
        Register Sluicio as an OIDC client in your IdP, using this redirect URI:
        <div
          className="mono"
          style={{ marginTop: 6, padding: "8px 10px", background: "var(--surface-2,#0f1424)", border: "1px solid var(--border,#26304d)", borderRadius: 6, fontSize: 12, wordBreak: "break-all" }}
        >
          {callbackURL}
        </div>
      </div>

      {providers === null ? (
        <p className="muted">Loading…</p>
      ) : providers.length === 0 ? (
        <p className="muted" style={{ fontSize: 13 }}>No identity providers configured yet.</p>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 8, marginBottom: 14 }}>
          {providers.map((p) => (
            <div key={p.id} style={{ border: "1px solid var(--border,#26304d)", borderRadius: 8, padding: 12 }}>
              <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
                <strong>{p.name}</strong>
                <span className={`pill pill--${p.enabled ? "ok" : "errors"}`}>{p.enabled ? "Enabled" : "Disabled"}</span>
                <span className="muted mono" style={{ fontSize: 12 }}>{p.issuer_url}</span>
                <span style={{ flex: 1 }} />
                <button className="btn" onClick={() => setDraft({ ...p, client_secret: "" })}>Edit</button>
                <button className="btn btn--danger" onClick={() => remove(p)}>Delete</button>
              </div>
              <MappingsEditor provider={p} groups={groups} onError={setError} />
            </div>
          ))}
        </div>
      )}

      {draft ? (
        <ProviderForm draft={draft} setDraft={setDraft} onSave={save} onCancel={() => setDraft(null)} />
      ) : (
        <button className="btn btn--primary" onClick={() => setDraft(blankDraft())}>+ Add provider</button>
      )}
    </div>
  );
}

function field(label: string, node: React.ReactNode, hint?: string) {
  return (
    <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 13 }}>
      <span style={{ fontWeight: 500 }}>{label}</span>
      {node}
      {hint && <span className="muted" style={{ fontSize: 11 }}>{hint}</span>}
    </label>
  );
}

function ProviderForm({
  draft,
  setDraft,
  onSave,
  onCancel,
}: {
  draft: Draft;
  setDraft: (d: Draft) => void;
  onSave: () => void;
  onCancel: () => void;
}) {
  const up = (patch: Partial<Draft>) => setDraft({ ...draft, ...patch });
  const input = (key: keyof Draft, ph?: string) => (
    <input
      className="input"
      value={(draft[key] as string) ?? ""}
      placeholder={ph}
      onChange={(e) => up({ [key]: e.target.value } as Partial<Draft>)}
    />
  );
  return (
    <div style={{ border: "1px solid var(--border,#26304d)", borderRadius: 8, padding: 14, display: "flex", flexDirection: "column", gap: 12, marginTop: 8 }}>
      <strong>{draft.id ? "Edit provider" : "New provider"}</strong>
      {field("Display name", input("name", "Acme SSO"), "Shown on the login button.")}
      {field("Issuer URL", input("issuer_url", "https://login.microsoftonline.com/<tenant>/v2.0"), "Sluicio reads /.well-known/openid-configuration from here.")}
      {field("Client ID", input("client_id"))}
      {field(
        "Client secret",
        input("client_secret", draft.id ? "•••••• (unchanged)" : ""),
        draft.id ? "Leave blank to keep the stored secret." : undefined,
      )}
      {field("Scopes", input("scopes"), "Space-separated. Include the groups scope your IdP needs (e.g. groups).")}
      {field("Groups claim", input("claim_groups"), "ID-token claim carrying the user's IdP groups/roles.")}
      <div style={{ display: "flex", gap: 12 }}>
        {field(
          "Default role",
          <select className="input" value={draft.default_role} onChange={(e) => up({ default_role: e.target.value as OrgRole })}>
            {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>,
          "For users with no matching mapping.",
        )}
        <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 13, marginTop: 22 }}>
          <input type="checkbox" checked={!!draft.jit_provisioning} onChange={(e) => up({ jit_provisioning: e.target.checked })} />
          Auto-create users (JIT)
        </label>
        <label style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 13, marginTop: 22 }}>
          <input type="checkbox" checked={!!draft.enabled} onChange={(e) => up({ enabled: e.target.checked })} />
          Enabled
        </label>
      </div>
      <details>
        <summary className="muted" style={{ fontSize: 12, cursor: "pointer" }}>Advanced claim names</summary>
        <div style={{ display: "flex", gap: 12, marginTop: 8 }}>
          {field("Email claim", input("claim_email"))}
          {field("Name claim", input("claim_name"))}
          {field("Subject claim", input("claim_sub"))}
        </div>
      </details>
      <div style={{ display: "flex", gap: 8 }}>
        <button className="btn btn--primary" onClick={onSave}>Save</button>
        <button className="btn" onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}

function MappingsEditor({ provider, groups, onError }: { provider: AuthProvider; groups: Group[]; onError: (m: string) => void }) {
  const [mappings, setMappings] = useState<ClaimMapping[] | null>(null);
  const [claimValue, setClaimValue] = useState("");
  const [orgRole, setOrgRole] = useState<OrgRole | "">("");
  const [groupId, setGroupId] = useState("");
  const [groupRole, setGroupRole] = useState<OrgRole>("viewer");

  const refresh = () => {
    api.listClaimMappings(provider.id).then((r) => setMappings(r.mappings)).catch((e) => onError(errMsg(e)));
  };
  // onError is the parent's setError (a stable useState setter), so it's safe
  // in the deps — the effect still only re-runs when the provider changes.
  useEffect(refresh, [provider.id, onError]);

  const add = async () => {
    if (!claimValue.trim()) return;
    if (!orgRole && !groupId) {
      onError("Set an org role and/or a team for the mapping.");
      return;
    }
    try {
      await api.createClaimMapping(provider.id, {
        claim_value: claimValue.trim(),
        org_role: orgRole || undefined,
        group_id: groupId || null,
        group_role: groupRole,
      });
      setClaimValue("");
      setOrgRole("");
      setGroupId("");
      refresh();
    } catch (e) {
      onError(errMsg(e));
    }
  };

  const del = async (mid: string, claimValue: string) => {
    if (!window.confirm(`Remove the access mapping for "${claimValue}"?`)) return;
    try {
      await api.deleteClaimMapping(provider.id, mid);
      refresh();
    } catch (e) {
      onError(errMsg(e));
    }
  };

  const groupName = (id?: string | null) => groups.find((g) => g.id === id)?.name ?? id ?? "";

  return (
    <div style={{ marginTop: 10, paddingTop: 10, borderTop: "1px solid var(--border,#26304d)" }}>
      <div className="muted" style={{ fontSize: 12, marginBottom: 6 }}>
        Claim → access mappings — when a user's <span className="mono">{provider.claim_groups}</span> claim contains the value, grant the role and/or team.
      </div>
      {mappings && mappings.length > 0 && (
        <div style={{ display: "flex", flexDirection: "column", gap: 4, marginBottom: 8 }}>
          {mappings.map((m) => (
            <div key={m.id} style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13 }}>
              <span className="mono">{m.claim_value}</span>
              <span className="muted">→</span>
              {m.org_role && <span className="pill">org-wide role: {m.org_role}</span>}
              {m.group_id && <span className="pill">team: {groupName(m.group_id)} · {m.group_role} in team</span>}
              <span style={{ flex: 1 }} />
              <button className="btn btn--sm" onClick={() => del(m.id, m.claim_value)}>Remove</button>
            </div>
          ))}
        </div>
      )}
      {/* Two independent grants per mapping: an org-wide role and/or a team
          membership with its own in-team role. Labelled columns so the two
          role selects can't be mistaken for one another. */}
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap", alignItems: "flex-end" }}>
        <label style={{ display: "flex", flexDirection: "column", gap: 2, fontSize: 11 }}>
          <span className="muted">IdP group value</span>
          <input className="input" style={{ width: 160 }} placeholder="e.g. sluicio-editors" value={claimValue} onChange={(e) => setClaimValue(e.target.value)} />
        </label>
        <label style={{ display: "flex", flexDirection: "column", gap: 2, fontSize: 11 }}>
          <span className="muted">Org-wide role</span>
          <select className="input" value={orgRole} onChange={(e) => setOrgRole(e.target.value as OrgRole | "")}>
            <option value="">none</option>
            {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
        </label>
        <label style={{ display: "flex", flexDirection: "column", gap: 2, fontSize: 11 }}>
          <span className="muted">Add to team</span>
          <select className="input" value={groupId} onChange={(e) => setGroupId(e.target.value)}>
            <option value="">none</option>
            {groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
          </select>
        </label>
        <label style={{ display: "flex", flexDirection: "column", gap: 2, fontSize: 11 }}>
          <span className="muted">Role in team</span>
          <select className="input" value={groupRole} onChange={(e) => setGroupRole(e.target.value as OrgRole)} disabled={!groupId}>
            {ROLES.map((r) => <option key={r} value={r}>{r}</option>)}
          </select>
        </label>
        <button className="btn btn--primary btn--sm" onClick={add}>Add mapping</button>
      </div>
    </div>
  );
}
