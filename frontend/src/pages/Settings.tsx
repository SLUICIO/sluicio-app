// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Settings — the org-level admin surface. Three tabs:
//
//   Members  — list / add / change role / remove members of the
//              current org. Mutations require an admin role; the
//              cell-api enforces the same.
//   Tokens   — personal access tokens for the current user. Mint,
//              copy once, revoke. The plaintext is surfaced exactly
//              once in a dialog at mint time.
//   SSO      — placeholder until the OIDC sign-in flow ships.
//              Documents the planned shape so customers know what to
//              expect.
//
// Each tab fetches its own data on mount; tab switching doesn't
// invalidate the other tabs' state.

import { FormEvent, Fragment, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type {
  AccessPolicy,
  AccessPolicyInput,
  AuthOrg,
  AuthRole,
  Group,
  GroupMember,
  IngestKey,
  MetricCatalogEntry,
  MemberRow,
  PolicyKind,
  PolicyExpr,
  PolicyExprOp,
  PolicyExprMatch,
  RetentionResponse,
  ServiceAccount,
  ServiceAccountGroup,
  ApiToken,
  SystemSettings,
} from "../api/types";
import { EditDrawer } from "../components/primitives";
import AlertEmailTemplateSettings from "../components/AlertEmailTemplateSettings";
import TrimIngestionPanel from "../components/metrics/TrimIngestionPanel";
import AnnouncementsAdmin from "../components/AnnouncementsAdmin";
import ConfigTransfer from "../components/ConfigTransfer";
import { EnterpriseBadge, UpgradeNotice } from "../components/EnterpriseUpsell";
import SsoSettings from "../components/SsoSettings";
import { formatRelative } from "../lib/format";
import { SYSTEM_KINDS } from "../lib/systemKinds";
import { useCurrentUser } from "../lib/useCurrentUser";
import { useLicense } from "../lib/useLicense";
import { usePageTitle } from "../lib/usePageTitle";
import type { AuditEntry, AuditVerifyResult, LicenseStatus, SMTPSettingsResponse } from "../api/types";

type TabKey =
  | "organization"
  | "members"
  | "service-accounts"
  | "groups"
  | "ingestion"
  | "retention"
  | "reports"
  | "system"
  | "sso"
  | "audit"
  | "license";

const TABS: { key: TabKey; label: string; subtitle: string; enterprise?: boolean }[] = [
  { key: "organization", label: "Organization", subtitle: "Org profile and announcements. Personal tokens and theme live under your Account." },
  { key: "members", label: "Members", subtitle: "People with access to this organization." },
  { key: "service-accounts", label: "Service accounts", subtitle: "Machine identities and their API tokens." },
  { key: "groups", label: "Groups", subtitle: "Group membership drives scoped access and team dashboards." },
  { key: "ingestion", label: "Ingestion", subtitle: "Ingest keys and ready-to-paste exporter configuration." },
  { key: "retention", label: "Retention", subtitle: "How long telemetry and audit history are kept, cell-wide." },
  { key: "reports", label: "Reports", subtitle: "Scheduled summaries delivered by email." },
  { key: "system", label: "System settings", subtitle: "Cell-wide knobs: environment label, ingest URL, email, security policy." },
  { key: "sso", label: "SSO", subtitle: "Single sign-on for this organization.", enterprise: true },
  { key: "audit", label: "Audit log", subtitle: "Who changed what, when — tamper-evident.", enterprise: true },
  { key: "license", label: "License", subtitle: "License status and Enterprise entitlements." },
];

// The left-nav groups, mirroring the settings information architecture:
// org-shaped things, data-shaped things, security, and cell platform.
const TAB_GROUPS: { label: string; keys: TabKey[] }[] = [
  { label: "Organization", keys: ["organization", "members", "service-accounts", "groups"] },
  { label: "Data", keys: ["ingestion", "retention", "reports"] },
  { label: "Security & access", keys: ["sso", "audit"] },
  { label: "Platform", keys: ["system", "license"] },
];

// TabIcon — minimal 16px glyphs for the settings nav, stroke follows
// text color so active/inactive states tint them for free.
function TabIcon({ k }: { k: TabKey }) {
  const common = { width: 15, height: 15, viewBox: "0 0 16 16", fill: "none", stroke: "currentColor", strokeWidth: 1.4, strokeLinecap: "round" as const, strokeLinejoin: "round" as const, "aria-hidden": true };
  switch (k) {
    case "organization":
      return <svg {...common}><path d="M2.5 13.5v-9l5.5-2.5 5.5 2.5v9M6.5 13.5v-3h3v3" /></svg>;
    case "members":
      return <svg {...common}><circle cx="5.5" cy="6" r="2.2" /><path d="M1.8 13.2c.5-2 1.9-3 3.7-3s3.2 1 3.7 3M10.5 4.2a2.2 2.2 0 0 1 0 3.9M11.6 10.4c1.3.4 2.2 1.3 2.6 2.8" /></svg>;
    case "service-accounts":
      return <svg {...common}><circle cx="6" cy="8" r="3" /><path d="M9 8h4.5M11.5 8v2.5" /></svg>;
    case "groups":
      return <svg {...common}><rect x="2.5" y="2.5" width="4.5" height="4.5" rx="1" /><rect x="9" y="2.5" width="4.5" height="4.5" rx="1" /><rect x="2.5" y="9" width="4.5" height="4.5" rx="1" /><rect x="9" y="9" width="4.5" height="4.5" rx="1" /></svg>;
    case "ingestion":
      return <svg {...common}><path d="M2 8h8M7.5 5.5 10 8l-2.5 2.5M12.5 3v10" /></svg>;
    case "retention":
      return <svg {...common}><circle cx="8" cy="8" r="5.5" /><path d="M8 5v3.2l2.2 1.3" /></svg>;
    case "reports":
      return <svg {...common}><path d="M4 2.5h6l2.5 2.5v8.5h-8.5zM5.8 8h4.4M5.8 10.5h4.4" /></svg>;
    case "system":
      return <svg {...common}><path d="M2.5 5h11M2.5 11h11" /><circle cx="6" cy="5" r="1.6" fill="var(--surface)" /><circle cx="10" cy="11" r="1.6" fill="var(--surface)" /></svg>;
    case "sso":
      return <svg {...common}><path d="M8 1.8 13 4v4c0 3.2-2.1 5.3-5 6.2C5.1 13.3 3 11.2 3 8V4z" /></svg>;
    case "audit":
      return <svg {...common}><path d="M3 3.5h10M3 6.5h10M3 9.5h6M3 12.5h4" /></svg>;
    case "license":
      return <svg {...common}><circle cx="8" cy="6.5" r="3.5" /><path d="M6 9.5 5 14l3-1.5L11 14l-1-4.5" /></svg>;
  }
}

// Cell-wide tabs — retention, system (SMTP + security), and license apply
// to every org on the cell, so only operators may see/change them. In
// single-org self-hosted the admin is the operator, so nothing hides there.
const OPERATOR_TABS: TabKey[] = ["retention", "system", "license"];

export default function Settings() {
  usePageTitle("Organization settings");
  const { isOperator } = useCurrentUser();
  const visibleTabs = TABS.filter((t) => isOperator || !OPERATOR_TABS.includes(t.key));
  // Tab lives in the URL (?tab=) so it's deep-linkable and survives copy-paste /
  // refresh / back-forward — not just read once on mount.
  const [searchParams, setSearchParams] = useSearchParams();
  const tabParam = searchParams.get("tab");
  const tab: TabKey = visibleTabs.some((x) => x.key === tabParam) ? (tabParam as TabKey) : "organization";
  const setTab = (key: TabKey) => {
    setSearchParams(
      (prev) => {
        prev.set("tab", key);
        return prev;
      },
      { replace: true },
    );
    // Each tab is its own page — start it at the top. The app's <main>
    // is the scroll container (not the window); resetting it also keeps
    // the sticky nav from inheriting a deep scroll position from the
    // previous (taller) tab.
    document.querySelector("main")?.scrollTo({ top: 0 });
  };

  const active = TABS.find((t) => t.key === tab)!;

  return (
    <div className="settings-layout">
      {/* Grouped settings nav (per the Sluicio Settings design): section
          labels + icon items + amber ENT badges; active item is a soft
          primary pill. Same ?tab= addressing and operator gating as the
          old horizontal strip — only the layout changed. */}
      <nav className="settings-nav" aria-label="Settings sections">
        {TAB_GROUPS.map((g) => {
          const items = g.keys
            .map((k) => visibleTabs.find((t) => t.key === k))
            .filter((t): t is (typeof TABS)[number] => Boolean(t));
          if (items.length === 0) return null;
          return (
            <div key={g.label}>
              <div className="settings-nav__label">{g.label}</div>
              {items.map((t) => (
                <button
                  key={t.key}
                  type="button"
                  className={`settings-nav__item ${tab === t.key ? "is-active" : ""}`}
                  onClick={() => setTab(t.key)}
                >
                  <TabIcon k={t.key} />
                  {t.label}
                  {t.enterprise && (
                    <span className="ent-badge" title="Sluicio Enterprise feature">
                      ENT
                    </span>
                  )}
                </button>
              ))}
            </div>
          );
        })}
      </nav>

      <div className="settings-content">
        <div style={{ marginBottom: 16 }}>
          <h1 className="page__title">{active.label}</h1>
          <p className="page__subtitle" style={{ marginTop: 2 }}>
            {active.key === "organization" ? (
              <>
                Org profile and announcements. Personal tokens and theme live under your{" "}
                <a href="/account">Account</a>.
              </>
            ) : (
              active.subtitle
            )}
          </p>
        </div>
        <div>
          {tab === "organization" && <OrganizationTab />}
          {tab === "members" && <MembersTab />}
          {tab === "service-accounts" && <ServiceAccountsTab />}
          {tab === "groups" && <GroupsTab />}
          {tab === "ingestion" && <IngestKeysTab />}
          {tab === "retention" && <RetentionTab />}
          {tab === "reports" && <ReportsTab />}
          {tab === "system" && (
            <>
              <SystemSettingsTab />
              <AlertEmailTemplateSettings />
              {/* Cell-wide announcements sit with the other cell-wide
                  settings (the whole tab is operator-gated). Org-scoped
                  announcements live on the Organization tab. */}
              <AnnouncementsAdmin scope="cell" />
            </>
          )}
          {tab === "sso" && <SsoTab />}
          {tab === "audit" && <AuditLogTab />}
          {tab === "license" && <LicenseTab />}
        </div>
      </div>
    </div>
  );
}

// ── Members tab ────────────────────────────────────────────────────────

// MemberDetails — the member blade. The table stays scannable (identity
// + role + recency); everything else lives here: account facts, security
// posture, and the admin actions (role change, password reset, removal).
function MemberDetails({
  member,
  isAdmin,
  isSelf,
  onChanged,
  onResetPassword,
  onRemoved,
}: {
  member: MemberRow;
  isAdmin: boolean;
  isSelf: boolean;
  onChanged: () => void;
  onResetPassword: () => void;
  onRemoved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const u = member.user;
  const dt = (v?: string | null, relative = false) =>
    v ? (relative ? formatRelative(v) : new Date(v).toLocaleString()) : "—";

  const Row = ({ label, children }: { label: string; children: React.ReactNode }) => (
    <div style={{ display: "flex", gap: 12, padding: "7px 0", borderBottom: "1px solid var(--border)", fontSize: 13 }}>
      <span className="muted" style={{ width: 150, flexShrink: 0 }}>{label}</span>
      <span style={{ minWidth: 0 }}>{children}</span>
    </div>
  );

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 16 }}>
      <div>
        <div className="mono" style={{ fontSize: 13 }}>{u.email}</div>
        {isSelf && <span className="muted" style={{ fontSize: 12 }}>This is you.</span>}
      </div>

      <div>
        <Row label="Role">
          {isAdmin && !isSelf ? (
            <RoleSelect
              value={member.role}
              onChange={async (next) => {
                try {
                  await api.updateMemberRole(u.id, next);
                  onChanged();
                } catch (e) {
                  alert(String((e as Error).message ?? e));
                }
              }}
            />
          ) : (
            <span className="badge">{member.role}</span>
          )}
        </Row>
        <Row label="Sign-in">
          {member.sso_providers.length === 0 && !member.has_password ? (
            "—"
          ) : (
            <span style={{ display: "inline-flex", gap: 4, flexWrap: "wrap" }}>
              {member.sso_providers.map((p) => (
                <span key={p} className="pill">{p}</span>
              ))}
              {member.has_password && <span className="muted">Password</span>}
            </span>
          )}
        </Row>
        <Row label="MFA">{u.mfa_enabled ? <span className="badge">Enabled</span> : <span className="muted">Off</span>}</Row>
        <Row label="Joined this org">{member.joined_at ? new Date(member.joined_at).toLocaleDateString() : "—"}</Row>
        <Row label="Account created">{dt(u.created_at)}</Row>
        <Row label="Last login">{dt(u.last_login_at, true)}</Row>
        <Row label="Last active">{dt(u.last_active_at, true)}</Row>
        <Row label="Successful logins">{u.login_count ?? 0}</Row>
        <Row label="Failed attempts">
          {u.failed_login_count ? (
            <span style={{ color: "var(--err-ink, #ef4444)", fontWeight: 600 }} title="Consecutive failed password attempts since last login">
              {u.failed_login_count}
            </span>
          ) : (
            "0"
          )}
        </Row>
        {u.must_reset_password && (
          <Row label="Password">
            <span className="muted">Must set a new password on next login</span>
          </Row>
        )}
      </div>

      {isAdmin && isSelf && (
        <div>
          <div className="muted" style={{ fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 8 }}>
            Admin actions
          </div>
          <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
            Not available on your own account — you can't remove yourself or
            admin-reset your own password. Your credentials live under{" "}
            <a href="/account">Account</a>. Admin actions appear here for other
            members.
          </p>
        </div>
      )}
      {isAdmin && !isSelf && (
        <div>
          <div className="muted" style={{ fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 8 }}>
            Admin actions
          </div>
          <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
            <button type="button" className="btn" onClick={onResetPassword} disabled={busy}>
              Reset password…
            </button>
            <button
              type="button"
              className="btn btn--danger"
              disabled={busy}
              onClick={async () => {
                if (!confirm(`Remove ${u.email} from this org?`)) return;
                setBusy(true);
                try {
                  await api.removeMember(u.id);
                  onRemoved();
                } catch (e) {
                  alert(String((e as Error).message ?? e));
                  setBusy(false);
                }
              }}
            >
              Remove from org
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

function MembersTab() {
  const { can, user: me } = useCurrentUser();
  const isAdmin = can("org.manage");
  const [members, setMembers] = useState<MemberRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [resetting, setResetting] = useState<MemberRow | null>(null);
  const [viewing, setViewing] = useState<MemberRow | null>(null);

  const refresh = () => {
    api
      .listMembers()
      .then((r) => setMembers(r.members))
      .catch((e) => setError(String((e as Error).message ?? e)));
  };
  useEffect(refresh, []);

  if (error)
    return <div className="alert alert--error">Failed to load: {error}</div>;
  if (!members) return <div className="placeholder">Loading…</div>;

  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 12 }}>
        <div className="muted" style={{ fontSize: 13 }}>
          {members.length} member{members.length === 1 ? "" : "s"} in this org
        </div>
        {isAdmin && (
          <button
            type="button"
            className="btn btn--primary"
            onClick={() => setAdding(true)}
            disabled={adding}
          >
            + Add member
          </button>
        )}
      </div>

      {adding && (
        <EditDrawer
          title="Add member"
          width="medium"
          onClose={() => setAdding(false)}
        >
          <AddMemberForm
            onClose={() => setAdding(false)}
            onCreated={() => {
              setAdding(false);
              refresh();
            }}
          />
        </EditDrawer>
      )}

      {resetting && (
        <EditDrawer
          title={`Reset password — ${resetting.user.name || resetting.user.email}`}
          width="narrow"
          onClose={() => setResetting(null)}
        >
          <ResetPasswordForm
            member={resetting}
            onClose={() => setResetting(null)}
            onDone={() => {
              setResetting(null);
              refresh();
            }}
          />
        </EditDrawer>
      )}

      <table className="table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Email</th>
            <th>Sign-in</th>
            <th>Role</th>
            <th>Joined</th>
            <th>Last login</th>
          </tr>
        </thead>
        <tbody>
          {members.map((m) => (
            <tr
              key={m.user.id}
              onClick={() => setViewing(m)}
              style={{ cursor: "pointer" }}
              title="Member details"
            >
              <td>{m.user.name || <span className="muted">—</span>}</td>
              <td className="mono">{m.user.email}</td>
              <td>
                {m.sso_providers.length === 0 && !m.has_password ? (
                  <span className="muted">—</span>
                ) : (
                  <span style={{ display: "inline-flex", gap: 4, flexWrap: "wrap", alignItems: "center" }}>
                    {m.sso_providers.map((p) => (
                      <span key={p} className="pill" title={`Signs in via ${p} (SSO)`}>
                        {p}
                      </span>
                    ))}
                    {m.has_password && (
                      <span className="muted" style={{ fontSize: 12 }}>Password</span>
                    )}
                  </span>
                )}
              </td>
              <td><span className="badge">{m.role}</span></td>
              <td>{m.joined_at ? new Date(m.joined_at).toLocaleDateString() : "—"}</td>
              <td
                title={
                  m.user.last_login_at
                    ? new Date(m.user.last_login_at).toLocaleString()
                    : undefined
                }
              >
                {m.user.last_login_at ? (
                  formatRelative(m.user.last_login_at)
                ) : (
                  <span className="muted">Never</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {viewing && (
        <EditDrawer
          title={viewing.user.name || viewing.user.email}
          width="medium"
          onClose={() => setViewing(null)}
        >
          <MemberDetails
            member={viewing}
            isAdmin={isAdmin}
            isSelf={viewing.user.id === me.id}
            onChanged={() => {
              refresh();
            }}
            onResetPassword={() => {
              setViewing(null);
              setResetting(viewing);
            }}
            onRemoved={() => {
              setViewing(null);
              refresh();
            }}
          />
        </EditDrawer>
      )}
    </div>
  );
}

function RoleSelect({ value, onChange }: { value: AuthRole; onChange: (r: AuthRole) => void }) {
  return (
    <select
      className="toolbar__select"
      value={value}
      onChange={(e) => onChange(e.target.value as AuthRole)}
    >
      <option value="admin">admin</option>
      <option value="editor">editor</option>
      <option value="viewer">viewer</option>
    </select>
  );
}

// ResetPasswordForm — admin sets a temporary password for another member.
// "Require change at next login" (default on) forces them into the
// ForcePasswordChange gate; either way their sessions are revoked so the
// new password takes effect immediately.
function ResetPasswordForm({
  member,
  onClose,
  onDone,
}: {
  member: MemberRow;
  onClose: () => void;
  onDone: () => void;
}) {
  const [pw, setPw] = useState("");
  const [requireChange, setRequireChange] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // A readable random temp password, so admins don't have to invent one.
  const generate = () => {
    const bytes = new Uint8Array(9);
    crypto.getRandomValues(bytes);
    const s = btoa(String.fromCharCode(...bytes)).replace(/[+/=]/g, "").slice(0, 12);
    setPw(s + "1!");
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (pw.length < 8) {
      setError("Password must be at least 8 characters.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await api.adminResetMemberPassword(member.user.id, pw, requireChange);
      onDone();
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setBusy(false);
    }
  };

  return (
    <form onSubmit={submit} className="form" style={{ display: "flex", flexDirection: "column", gap: 12 }}>
      <p className="muted" style={{ fontSize: 13, margin: 0 }}>
        Set a temporary password for <strong>{member.user.email}</strong>. Share
        it with them over a secure channel — their existing sessions are signed
        out immediately.
      </p>
      <label className="form__label">
        Temporary password
        <div style={{ display: "flex", gap: 8 }}>
          <input
            className="input mono"
            type="text"
            autoComplete="off"
            value={pw}
            onChange={(e) => setPw(e.target.value)}
            style={{ flex: 1 }}
          />
          <button type="button" className="btn btn--sm" onClick={generate}>
            Generate
          </button>
        </div>
      </label>
      <label style={{ display: "flex", alignItems: "center", gap: 8, fontSize: 13, cursor: "pointer" }}>
        <input
          type="checkbox"
          checked={requireChange}
          onChange={(e) => setRequireChange(e.target.checked)}
        />
        Require the user to change it at next login
      </label>
      {error && <div className="alert alert--error">{error}</div>}
      <div style={{ display: "flex", gap: 8 }}>
        <button type="submit" className="btn btn--primary" disabled={busy}>
          {busy ? "Setting…" : "Set temporary password"}
        </button>
        <button type="button" className="btn" onClick={onClose} disabled={busy}>
          Cancel
        </button>
      </div>
    </form>
  );
}


function AddMemberForm({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<AuthRole>("editor");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.addMember({ email, name, password, role });
      onCreated();
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setBusy(false);
    }
  };

  return (
    // Rendered inside an EditDrawer body which provides the surface +
    // padding, so this is just .form (no .card wrapper, no manual
    // background / border).
    <form onSubmit={submit} className="form">
      <div className="form__row">
        <label className="form__label">
          Email
          <input
            type="email"
            className="search__input"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="user@example.com"
            required
            autoFocus
          />
        </label>
        <label className="form__label">
          Name
          <input
            className="search__input"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Display name"
          />
        </label>
      </div>
      <div className="form__row">
        <label className="form__label">
          Initial password
          <input
            type="password"
            className="search__input"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            minLength={8}
            required
          />
          <span className="form__hint">
            The user changes this after first sign-in. Min 8 characters.
          </span>
        </label>
        <label className="form__label">
          Role
          <select
            className="toolbar__select"
            value={role}
            onChange={(e) => setRole(e.target.value as AuthRole)}
          >
            <option value="admin">admin — full org control</option>
            <option value="editor">editor — mutate resources</option>
            <option value="viewer">viewer — read-only</option>
          </select>
        </label>
      </div>
      {error && <div className="alert alert--error">{error}</div>}
      <div className="form__actions">
        <button type="button" className="btn" onClick={onClose} disabled={busy}>
          Cancel
        </button>
        <button type="submit" className="btn btn--primary" disabled={busy || !email || password.length < 8}>
          {busy ? "Adding…" : "Add member"}
        </button>
      </div>
    </form>
  );
}

// ── Organization tab ───────────────────────────────────────────────────
//
// Read-and-edit org profile (name + slug) + the destructive delete-org
// button at the bottom. Org admins see everything as editable; non-
// admins see read-only fields with disabled inputs (the backend
// enforces the same on PATCH/DELETE).

function OrganizationTab() {
  const { can, organization } = useCurrentUser();
  const isAdmin = can("org.manage");
  const [org, setOrg] = useState<AuthOrg | null>(null);
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    api
      .getOrg(organization.id)
      .then((o) => {
        setOrg(o);
        setName(o.name);
        setSlug(o.slug);
      })
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, [organization.id]);

  if (error) return <div className="alert alert--error">Failed to load: {error}</div>;
  if (!org) return <div className="placeholder">Loading…</div>;

  const dirty = name.trim() !== org.name || slug.trim() !== org.slug;

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const updated = await api.updateOrg(org.id, {
        ...(name.trim() !== org.name ? { name: name.trim() } : {}),
        ...(slug.trim() !== org.slug ? { slug: slug.trim() } : {}),
      });
      setOrg(updated);
      setName(updated.name);
      setSlug(updated.slug);
      setSavedAt(Date.now());
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div>
      <form className="form" onSubmit={save} style={{ maxWidth: 480 }}>
        <label className="form__label">
          Organization name
          <input
            className="search__input"
            value={name}
            onChange={(e) => setName(e.target.value)}
            disabled={!isAdmin}
            required
          />
          <span className="form__hint">Shown in the user menu + member lists.</span>
        </label>
        <label className="form__label">
          Slug
          <input
            className="search__input mono"
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            disabled={!isAdmin}
            pattern="[a-z0-9-]+"
            required
          />
          <span className="form__hint">
            Lowercase letters, digits, hyphens. Changes URL paths — bookmarks may break.
          </span>
        </label>
        <div className="form__label">
          <span>Created</span>
          <div className="muted" style={{ fontSize: 13 }}>
            {org.created_at ? new Date(org.created_at).toLocaleString() : "—"}
          </div>
        </div>
        {error && <div className="alert alert--error">{error}</div>}
        {savedAt && !error && (
          <div className="alert alert--ok">Saved.</div>
        )}
        {isAdmin && (
          <div className="form__actions">
            <button type="submit" className="btn btn--primary" disabled={busy || !dirty || !name.trim() || !slug.trim()}>
              {busy ? "Saving…" : "Save changes"}
            </button>
          </div>
        )}
      </form>

      {isAdmin && <AnnouncementsAdmin scope="org" />}

      {isAdmin && <ConfigTransfer />}

      {isAdmin && (
        <p className="muted" style={{ marginTop: 32, fontSize: 12, maxWidth: 600 }}>
          Deleting an organization is a cell-operator action — it lives on the
          Operator page, not here.
        </p>
      )}
    </div>
  );
}

// ── Ingestion (OTLP ingest keys) tab ───────────────────────────────────
//
// Per-org keys that authenticate telemetry at cell-ingest. The full key
// is shown exactly once at creation; listing only shows the masked
// prefix. Generate/revoke are admin-only (matches the backend gate).

// KeySnippet renders a labelled, copyable config block (the SDK env-var
// form and the Collector YAML form share this). `hint` is rendered below
// the code as small print.
function KeySnippet({
  title,
  code,
  hint,
}: {
  title: string;
  code: string;
  hint?: React.ReactNode;
}) {
  return (
    <div style={{ marginTop: 14 }}>
      <div style={{ fontSize: 12, marginBottom: 6, display: "flex", alignItems: "center", gap: 8 }}>
        <span style={{ fontWeight: 600 }}>{title}</span>
        <button
          type="button"
          className="btn"
          style={{ marginLeft: "auto" }}
          onClick={() => navigator.clipboard?.writeText(code)}
        >
          Copy snippet
        </button>
      </div>
      <pre
        className="mono"
        style={{
          margin: 0,
          background: "var(--surface)",
          padding: "10px 12px",
          borderRadius: 6,
          border: "1px solid var(--border)",
          overflowX: "auto",
          fontSize: 12,
          lineHeight: 1.5,
        }}
      >
        {code}
      </pre>
      {hint && (
        <div className="muted" style={{ fontSize: 11, marginTop: 6 }}>
          {hint}
        </div>
      )}
    </div>
  );
}

// ── Service accounts tab ───────────────────────────────────────────────
// Machine identities (their own role) + the bearer tokens they own. The
// minted token plaintext is shown exactly once.
function ServiceAccountsTab() {
  const { can } = useCurrentUser();
  const isAdmin = can("org.manage");
  const [sas, setSas] = useState<ServiceAccount[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [role, setRole] = useState("editor");
  const [scope, setScope] = useState("scoped");
  const [busy, setBusy] = useState(false);
  const [minted, setMinted] = useState<{ key: string; sa: string } | null>(null);
  const [open, setOpen] = useState<{ id: string; panel: "tokens" | "access" } | null>(null);

  const refresh = () =>
    api.listServiceAccounts().then((r) => setSas(r.service_accounts)).catch((e) => setError(String((e as Error).message ?? e)));
  useEffect(() => {
    refresh();
  }, []);

  const create = async () => {
    if (!name.trim()) return;
    setBusy(true);
    setError(null);
    try {
      await api.createServiceAccount({ name: name.trim(), description: desc.trim(), role, scope });
      setName("");
      setDesc("");
      refresh();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (sa: ServiceAccount) => {
    if (!confirm(`Delete service account "${sa.name}"? Its tokens are revoked immediately.`)) return;
    try {
      await api.deleteServiceAccount(sa.id);
      if (open?.id === sa.id) setOpen(null);
      refresh();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  if (error) return <div className="alert alert--error">Failed to load: {error}</div>;
  if (!sas) return <div className="placeholder">Loading…</div>;

  return (
    <div>
      <div className="muted" style={{ fontSize: 13, marginBottom: 12 }}>
        Service accounts are machine identities for automation (CI/CD, integrations) — each has its own role and
        owns API tokens used as <code>Authorization: Bearer &lt;token&gt;</code>. Unlike a personal token, a service
        account isn't tied to a person, so it survives staff changes. Org-admin only.
      </div>

      {minted && (
        <div className="card" style={{ padding: 14, marginBottom: 16, borderColor: "var(--primary)" }}>
          <div style={{ fontWeight: 600, marginBottom: 4 }}>New token for “{minted.sa}” — copy it now</div>
          <div className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
            This is the only time the full token is shown. Store it securely.
          </div>
          <div className="mono" style={{ wordBreak: "break-all", background: "var(--surface)", padding: "8px 10px", borderRadius: 6, border: "1px solid var(--border)" }}>
            {minted.key}
          </div>
          <div style={{ display: "flex", gap: 8, marginTop: 8 }}>
            <button type="button" className="btn" onClick={() => navigator.clipboard?.writeText(minted.key)}>Copy token</button>
            <button type="button" className="btn btn--link" style={{ marginLeft: "auto" }} onClick={() => setMinted(null)}>Dismiss</button>
          </div>
        </div>
      )}

      {isAdmin && (
        <div className="card" style={{ padding: 14, marginBottom: 16, display: "flex", gap: 8, alignItems: "flex-end", flexWrap: "wrap" }}>
          <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12 }}>
            Name
            <input className="search__input" style={{ minWidth: 180 }} placeholder="ci-bot" value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12 }}>
            Role
            <select className="toolbar__select" value={role} onChange={(e) => setRole(e.target.value)}>
              <option value="viewer">viewer</option>
              <option value="editor">editor</option>
              <option value="admin">admin</option>
            </select>
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12 }}>
            Visibility
            <select className="toolbar__select" value={scope} onChange={(e) => setScope(e.target.value)} title="What this account's tokens can read">
              <option value="scoped">Scoped (via groups)</option>
              <option value="org_wide">Org-wide read</option>
            </select>
          </label>
          <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12, flex: 1, minWidth: 200 }}>
            Description
            <input className="search__input" placeholder="optional" value={desc} onChange={(e) => setDesc(e.target.value)} />
          </label>
          <button type="button" className="btn btn--primary" disabled={busy || !name.trim()} onClick={create}>Create</button>
          {role === "admin" && (
            <div style={{ flexBasis: "100%", fontSize: 12, color: "var(--warn)" }}>
              ⚠ An <strong>admin</strong> service account can do anything in the org — manage members, tokens, and
              settings. Its tokens are durable admin credentials; prefer the least role the automation needs
              (read-only for dashboards / MCP).
            </div>
          )}
          {scope === "scoped" && role !== "admin" && (
            <div className="muted" style={{ flexBasis: "100%", fontSize: 12 }}>
              A scoped account sees <strong>nothing</strong> until you add it to a group — its group memberships
              define exactly which services its tokens can read (open “Access” after creating).
            </div>
          )}
          {scope === "org_wide" && role !== "admin" && (
            <div style={{ flexBasis: "100%", fontSize: 12, color: "var(--warn)" }}>
              ⚠ An <strong>org-wide</strong> account's tokens read every service, log, trace and message in the org
              — hand one to an external tool or AI assistant and it sees everything. Prefer scoped + group
              memberships.
            </div>
          )}
        </div>
      )}

      {sas.length === 0 ? (
        <div className="placeholder">No service accounts yet.{isAdmin ? " Create one above." : ""}</div>
      ) : (
        <div className="card" style={{ padding: "4px 16px 8px" }}>
          {sas.map((sa, i) => (
            <div key={sa.id} style={{ borderTop: i === 0 ? undefined : "1px solid var(--border)", padding: "10px 0" }}>
              <div style={{ display: "flex", alignItems: "center", gap: 10, flexWrap: "wrap" }}>
                <span style={{ fontWeight: 600 }}>{sa.name}</span>
                <span className="badge-brand">{sa.role}</span>
                {sa.scope === "org_wide" ? (
                  <span
                    className="badge"
                    style={{ background: "color-mix(in srgb, var(--warn) 18%, transparent)", color: "var(--warn)", borderColor: "var(--warn)" }}
                    title="This account's tokens read every service in the org"
                  >
                    org-wide read
                  </span>
                ) : (
                  <span className="badge" title="Sees only what its group memberships grant">scoped</span>
                )}
                {sa.description && <span className="muted" style={{ fontSize: 12 }}>{sa.description}</span>}
                {isAdmin && (
                  <span style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
                    <button
                      type="button"
                      className="btn btn--sm"
                      onClick={() => setOpen(open?.id === sa.id && open.panel === "access" ? null : { id: sa.id, panel: "access" })}
                    >
                      {open?.id === sa.id && open.panel === "access" ? "Hide access" : "Access"}
                    </button>
                    <button
                      type="button"
                      className="btn btn--sm"
                      onClick={() => setOpen(open?.id === sa.id && open.panel === "tokens" ? null : { id: sa.id, panel: "tokens" })}
                    >
                      {open?.id === sa.id && open.panel === "tokens" ? "Hide tokens" : "Tokens"}
                    </button>
                    <button type="button" className="btn btn--sm btn--danger" onClick={() => remove(sa)}>Delete</button>
                  </span>
                )}
              </div>
              {open?.id === sa.id && open.panel === "tokens" && (
                <ServiceAccountTokens
                  sa={sa}
                  onMinted={(key) => setMinted({ key, sa: sa.name })}
                  onError={(m) => setError(m)}
                />
              )}
              {open?.id === sa.id && open.panel === "access" && (
                <ServiceAccountAccess sa={sa} onChanged={refresh} onError={(m) => setError(m)} />
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function ServiceAccountTokens({ sa, onMinted, onError }: { sa: ServiceAccount; onMinted: (key: string) => void; onError: (m: string) => void }) {
  const [toks, setToks] = useState<ApiToken[] | null>(null);
  const [name, setName] = useState("");
  const [scope, setScope] = useState("");
  const [exp, setExp] = useState(0);
  const [busy, setBusy] = useState(false);

  const refresh = () =>
    api.listServiceAccountTokens(sa.id).then((r) => setToks(r.tokens)).catch((e) => onError(String((e as Error).message ?? e)));
  useEffect(() => {
    refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sa.id]);

  const mint = async () => {
    if (!name.trim()) return;
    setBusy(true);
    try {
      const res = await api.createServiceAccountToken(sa.id, name.trim(), scope, exp);
      onMinted(res.plaintext);
      setName("");
      refresh();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  // Rotate = reissue with the same name + access cap (no expiry), then revoke
  // the old token. Surfaces the new secret once.
  const rotate = async (t: ApiToken) => {
    if (!confirm(`Rotate "${t.name}"? A new token is issued and the current one is revoked immediately.`)) return;
    setBusy(true);
    try {
      const res = await api.createServiceAccountToken(sa.id, t.name, t.scope_role ?? "", 0);
      onMinted(res.plaintext);
      await api.revokeServiceAccountToken(sa.id, t.id);
      refresh();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (t: ApiToken) => {
    if (!confirm(`Revoke token "${t.name}"? Callers using it will start getting 401s.`)) return;
    try {
      await api.revokeServiceAccountToken(sa.id, t.id);
      refresh();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    }
  };

  return (
    <div style={{ marginLeft: 8, marginTop: 8, paddingLeft: 12, borderLeft: "2px solid var(--border)", display: "flex", flexDirection: "column", gap: 6 }}>
      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap" }}>
        <input className="search__input" style={{ maxWidth: 220 }} placeholder="token name (e.g. deploy key)" value={name} onChange={(e) => setName(e.target.value)} />
        <select className="toolbar__select" value={scope} onChange={(e) => setScope(e.target.value)} title="Cap the token below the account's role">
          <option value="">Access: full ({sa.role})</option>
          <option value="editor">Access: editor (write)</option>
          <option value="viewer">Access: read-only</option>
        </select>
        <select className="toolbar__select" value={exp} onChange={(e) => setExp(Number(e.target.value))} title="Token expiry">
          <option value={0}>Expires: never</option>
          <option value={30}>Expires: 30 days</option>
          <option value={90}>Expires: 90 days</option>
          <option value={365}>Expires: 1 year</option>
        </select>
        <button type="button" className="btn btn--sm btn--primary" disabled={busy || !name.trim()} onClick={mint}>New token</button>
      </div>
      {toks === null ? (
        <div className="muted" style={{ fontSize: 12 }}>Loading…</div>
      ) : toks.length === 0 ? (
        <div className="muted" style={{ fontSize: 12 }}>No tokens yet.</div>
      ) : (
        toks.map((t) => (
          <div key={t.id} style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 12.5 }}>
            <span className="mono">{t.prefix}…</span>
            <span>{t.name}</span>
            <span className="badge-brand">{t.scope_role ? t.scope_role : "full"}</span>
            <span className="muted">{t.expires_at ? `expires ${formatRelative(t.expires_at)}` : "no expiry"}</span>
            <span className="muted">{t.last_used_at ? `used ${formatRelative(t.last_used_at)}` : "never used"}</span>
            <span style={{ marginLeft: "auto", display: "flex", gap: 8 }}>
              <button type="button" className="btn btn--sm" disabled={busy} onClick={() => rotate(t)}>Rotate</button>
              <button type="button" className="btn btn--sm btn--danger" disabled={busy} onClick={() => revoke(t)}>Revoke</button>
            </span>
          </div>
        ))
      )}
    </div>
  );
}

// ServiceAccountAccess — the visibility editor for one service account:
// its scope (scoped vs org-wide) and, for scoped accounts, the group
// memberships that define what its tokens can see
// (docs/service-account-scoping-design.md).
function ServiceAccountAccess({ sa, onChanged, onError }: { sa: ServiceAccount; onChanged: () => void; onError: (m: string) => void }) {
  const [groups, setGroups] = useState<ServiceAccountGroup[] | null>(null);
  const [allGroups, setAllGroups] = useState<Group[]>([]);
  const [addId, setAddId] = useState("");
  const [addRole, setAddRole] = useState<AuthRole>("viewer");
  const [busy, setBusy] = useState(false);

  const refresh = () => {
    Promise.all([api.listServiceAccountGroups(sa.id), api.listGroups()])
      .then(([sg, g]) => {
        setGroups(sg.groups);
        setAllGroups(g.groups);
      })
      .catch((e) => onError(String((e as Error).message ?? e)));
  };
  useEffect(() => {
    refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sa.id]);

  const setScope = async (scope: "scoped" | "org_wide") => {
    if (scope === sa.scope) return;
    if (
      scope === "org_wide" &&
      !confirm(`Make "${sa.name}" org-wide? Its tokens will read EVERY service, log, trace and message in the org.`)
    )
      return;
    setBusy(true);
    try {
      await api.updateServiceAccount(sa.id, { name: sa.name, description: sa.description, role: sa.role, scope });
      onChanged();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const inGroups = new Set((groups ?? []).map((g) => g.group.id));
  const addable = allGroups.filter((g) => !inGroups.has(g.id));

  const add = async () => {
    if (!addId) return;
    setBusy(true);
    try {
      await api.addGroupMember(addId, { service_account_id: sa.id, role: addRole });
      setAddId("");
      refresh();
    } catch (e) {
      onError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ marginLeft: 8, marginTop: 8, paddingLeft: 12, borderLeft: "2px solid var(--border)", display: "flex", flexDirection: "column", gap: 10, fontSize: 12.5 }}>
      <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        <label style={{ display: "flex", gap: 8, alignItems: "baseline" }}>
          <input type="radio" name={`sa-scope-${sa.id}`} checked={sa.scope !== "org_wide"} disabled={busy} onChange={() => setScope("scoped")} />
          <span>
            <strong>Scoped</strong> (recommended) — tokens see only what this account's group memberships grant,
            exactly like a user. No groups → sees nothing.
          </span>
        </label>
        <label style={{ display: "flex", gap: 8, alignItems: "baseline" }}>
          <input type="radio" name={`sa-scope-${sa.id}`} checked={sa.scope === "org_wide"} disabled={busy} onChange={() => setScope("org_wide")} />
          <span>
            <strong style={{ color: "var(--warn)" }}>Org-wide read</strong> — tokens read every service in the org.
            For trusted platform automation only.
          </span>
        </label>
      </div>

      <div>
        <div style={{ fontWeight: 600, marginBottom: 4 }}>
          Group memberships
          {sa.scope === "org_wide" && (
            <span className="muted" style={{ fontWeight: 400, marginLeft: 8 }}>
              (kept, but inactive while the account is org-wide)
            </span>
          )}
        </div>
        {groups === null ? (
          <div className="muted">Loading…</div>
        ) : groups.length === 0 ? (
          <div className="muted">
            No groups yet{sa.scope !== "org_wide" ? " — this account currently sees nothing" : ""}.
          </div>
        ) : (
          groups.map((g) => (
            <div key={g.group.id} style={{ display: "flex", alignItems: "center", gap: 10, padding: "3px 0" }}>
              <span>{g.group.name}</span>
              <span className="badge-brand">{g.role}</span>
              <button
                type="button"
                className="btn btn--link"
                style={{ color: "var(--err-ink, #ef4444)", marginLeft: "auto" }}
                disabled={busy}
                onClick={async () => {
                  if (!confirm(`Remove "${sa.name}" from "${g.group.name}"?`)) return;
                  setBusy(true);
                  try {
                    await api.removeGroupServiceAccount(g.group.id, sa.id);
                    refresh();
                  } catch (e) {
                    onError(String((e as Error).message ?? e));
                  } finally {
                    setBusy(false);
                  }
                }}
              >
                Remove
              </button>
            </div>
          ))
        )}
        {addable.length > 0 && (
          <div style={{ display: "flex", gap: 8, alignItems: "center", marginTop: 6 }}>
            <select className="toolbar__select" value={addId} onChange={(e) => setAddId(e.target.value)}>
              <option value="">Add to group…</option>
              {addable.map((g) => (
                <option key={g.id} value={g.id}>{g.name}</option>
              ))}
            </select>
            <select className="toolbar__select" value={addRole} onChange={(e) => setAddRole(e.target.value as AuthRole)} title="Role within the group">
              <option value="viewer">viewer</option>
              <option value="editor">editor</option>
            </select>
            <button type="button" className="btn btn--sm" disabled={busy || !addId} onClick={add}>Add</button>
          </div>
        )}
      </div>
    </div>
  );
}

function IngestKeysTab() {
  const { can } = useCurrentUser();
  const isAdmin = can("org.manage");
  const [keys, setKeys] = useState<IngestKey[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<{ key: string; name: string } | null>(null);
  // The OTLP/HTTP base URL we bake into the exporter snippets. Prefer the
  // admin-configured ingest URL (System settings); fall back to the host
  // the browser is on, which is correct for single-host deployments.
  const [ingestBase, setIngestBase] = useState(window.location.origin);
  // null = still loading; false = falling back to the browser origin,
  // which is WRONG whenever ingest runs on its own hostname.
  const [ingestConfigured, setIngestConfigured] = useState<boolean | null>(null);

  const refresh = () => {
    api
      .listIngestKeys()
      .then((r) => setKeys(r.keys))
      .catch((e) => setError(String((e as Error).message ?? e)));
  };
  useEffect(refresh, []);
  useEffect(() => {
    api
      .getSystemSettings()
      .then((s) => {
        if (s.ingest_base_url) setIngestBase(s.ingest_base_url);
        setIngestConfigured(Boolean(s.ingest_base_url));
      })
      .catch(() => {
        /* non-fatal: snippets keep the browser-origin default */
      });
  }, []);

  const create = async () => {
    if (!name.trim()) return;
    setBusy(true);
    setError(null);
    try {
      const res = await api.createIngestKey(name.trim());
      setCreated({ key: res.key, name: res.meta.name });
      setName("");
      refresh();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const revoke = async (k: IngestKey) => {
    if (!confirm(`Revoke ingest key "${k.name}"? Collectors using it will start getting 401s.`)) return;
    try {
      await api.revokeIngestKey(k.id);
      refresh();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    }
  };

  if (error) return <div className="alert alert--error">Failed to load: {error}</div>;
  if (!keys) return <div className="placeholder">Loading…</div>;

  // A paste-ready OpenTelemetry Collector config: the otlphttp exporter
  // pointed at this cell (ingestBase = configured ingest URL or the
  // browser origin) with the real key baked in, plus the pipeline wiring
  // so traces/metrics/logs actually export.
  const collectorSnippet = (key: string) =>
    [
      "exporters:",
      "  otlphttp:",
      `    endpoint: ${ingestBase}`,
      "    headers:",
      `      Authorization: "Bearer ${key}"`,
      "",
      "service:",
      "  pipelines:",
      "    traces:  { exporters: [otlphttp] }",
      "    metrics: { exporters: [otlphttp] }",
      "    logs:    { exporters: [otlphttp] }",
    ].join("\n");

  // The OpenTelemetry SDK env-var form, for apps instrumented directly
  // (no Collector). The SDK appends /v1/{traces,logs,metrics} to the
  // endpoint, so it's the bare ingest host. Key baked in.
  const sdkSnippet = (key: string) =>
    [
      `OTEL_EXPORTER_OTLP_ENDPOINT=${ingestBase}`,
      "OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
      `OTEL_EXPORTER_OTLP_HEADERS=Authorization=Bearer ${key}`,
    ].join("\n");

  return (
    <div>
      <div className="muted" style={{ fontSize: 13, marginBottom: 12 }}>
        Ingest keys authenticate your OpenTelemetry collectors. Send the key as{" "}
        <code>Authorization: Bearer &lt;key&gt;</code> to the cell's OTLP/HTTP endpoint;
        telemetry without a valid key is rejected. Keep keys secret — anyone with one
        can write telemetry as this organization.
      </div>

      {ingestConfigured === false && (
        <div className="alert alert--warn" style={{ marginBottom: 12, fontSize: 13 }}>
          Exporter snippets currently point at <code>{ingestBase}</code> — this UI's own
          host. If OTLP ingest is served on its own hostname (e.g.{" "}
          <code>demo-ingest.example.com</code>), set the <strong>Ingest URL</strong>{" "}
          under{" "}
          <Link to="/settings?tab=system" style={{ color: "inherit", fontWeight: 600, textDecoration: "underline" }}>
            Settings → System settings
          </Link>{" "}
          so keys ship with the correct endpoint.
        </div>
      )}

      {created && (
        <div className="card" style={{ padding: 14, marginBottom: 16, borderColor: "var(--primary)" }}>
          <div style={{ fontWeight: 600, marginBottom: 4 }}>New key “{created.name}” — copy it now</div>
          <div className="muted" style={{ fontSize: 12, marginBottom: 8 }}>
            This is the only time the full key is shown. Store it securely.
          </div>
          <div
            className="mono"
            style={{ wordBreak: "break-all", background: "var(--surface)", padding: "8px 10px", borderRadius: 6, border: "1px solid var(--border)" }}
          >
            {created.key}
          </div>
          <div style={{ display: "flex", gap: 8, marginTop: 8, alignItems: "center" }}>
            <button type="button" className="btn" onClick={() => navigator.clipboard?.writeText(created.key)}>
              Copy key
            </button>
            <button type="button" className="btn btn--link" style={{ marginLeft: "auto" }} onClick={() => setCreated(null)}>
              Dismiss
            </button>
          </div>

          <div className="muted" style={{ fontSize: 12, marginTop: 14 }}>
            Ready-to-paste exporter config with this key already filled in. Pick whichever
            matches how your app ships telemetry. The endpoint is{" "}
            <code>{ingestBase}</code>
            {ingestBase === window.location.origin ? (
              <>
                {" "}— derived from this page's host. If your collector reaches
                cell-ingest at a different address, set the{" "}
                <strong>ingest base URL</strong> under the System tab.
              </>
            ) : (
              <> — the configured ingest base URL (System tab).</>
            )}
          </div>

          <KeySnippet
            title="OpenTelemetry SDK (env vars)"
            code={sdkSnippet(created.key)}
            hint={
              <>
                For apps instrumented directly with an OpenTelemetry SDK — no Collector needed.
              </>
            }
          />

          <KeySnippet
            title="OpenTelemetry Collector (otel-collector.yaml)"
            code={collectorSnippet(created.key)}
            hint={<>For a Collector pipeline fanning traces, metrics and logs to Sluicio.</>}
          />
        </div>
      )}

      {isAdmin && (
        <div style={{ display: "flex", gap: 8, alignItems: "flex-end", marginBottom: 16 }}>
          <label style={{ display: "flex", flexDirection: "column", gap: 4, fontSize: 12 }}>
            <span className="muted">New key name</span>
            <input
              className="search__input"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="prod collector"
            />
          </label>
          <button type="button" className="btn btn--primary" disabled={busy || !name.trim()} onClick={create}>
            {busy ? "Generating…" : "Generate key"}
          </button>
        </div>
      )}

      {keys.length === 0 ? (
        <div className="placeholder">
          No ingest keys yet.{" "}
          {isAdmin ? "Generate one to start sending telemetry." : "Ask an org admin to create one."}
        </div>
      ) : (
        <table className="table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Key</th>
              <th>Created</th>
              <th>Last used</th>
              {isAdmin && <th></th>}
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => (
              <tr key={k.id}>
                <td>{k.name}</td>
                <td className="mono muted">{k.prefix}…</td>
                <td className="muted">{new Date(k.created_at).toLocaleString()}</td>
                <td className="muted">{k.last_used_at ? new Date(k.last_used_at).toLocaleString() : "—"}</td>
                {isAdmin && (
                  <td>
                    <button type="button" className="btn btn--sm btn--danger" onClick={() => revoke(k)}>
                      Revoke
                    </button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

// ── Groups tab ─────────────────────────────────────────────────────────
//
// Manages org groups (the second access-control axis under org). Org
// admins see the create / edit / delete actions; everyone else sees a
// read-only list. Clicking a group opens an inline detail panel that
// lists its members + lets admins add/remove + change roles.

function GroupsTab() {
  const { can } = useCurrentUser();
  const isAdmin = can("org.manage");
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [selected, setSelected] = useState<Group | null>(null);

  const refresh = () => {
    api
      .listGroups()
      .then((r) => setGroups(r.groups))
      .catch((e) => setError(String((e as Error).message ?? e)));
  };
  useEffect(refresh, []);

  if (error)
    return <div className="alert alert--error">Failed to load: {error}</div>;
  if (!groups) return <div className="placeholder">Loading…</div>;

  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 12 }}>
        <div className="muted" style={{ fontSize: 13 }}>
          Groups are how the org divides who-sees-what. Each group has
          members + assigned services; non-admin users only see services
          in groups they belong to. Org admins always see everything.
        </div>
        {isAdmin && (
          <button type="button" className="btn btn--primary" onClick={() => setCreating(true)}>
            + New group
          </button>
        )}
      </div>

      {creating && (
        <EditDrawer
          title="New group"
          width="narrow"
          onClose={() => setCreating(false)}
        >
          <CreateGroupForm
            onClose={() => setCreating(false)}
            onCreated={() => {
              setCreating(false);
              refresh();
            }}
          />
        </EditDrawer>
      )}

      {groups.length === 0 ? (
        <div className="placeholder">
          No groups yet.{" "}
          {isAdmin
            ? <>Click <b>+ New group</b> to create one.</>
            : "Ask an org admin to create one and add you."}
        </div>
      ) : (
        <table className="table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Slug</th>
              <th>Description</th>
              <th>Members</th>
              <th>Services</th>
            </tr>
          </thead>
          <tbody>
            {groups.map((g) => (
              <tr
                key={g.id}
                onClick={() => setSelected(g)}
                style={{ cursor: "pointer" }}
                title="Group details"
              >
                <td className="font-medium">{g.name}</td>
                <td className="mono">{g.slug}</td>
                <td>{g.description || <span className="muted">—</span>}</td>
                <td>{g.member_count}</td>
                <td>{g.service_count}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {selected && (
        <EditDrawer
          title={
            <>
              {selected.name}{" "}
              <span className="muted mono" style={{ fontWeight: 400, fontSize: 12 }}>
                · {selected.slug}
              </span>
            </>
          }
          width="medium"
          onClose={() => setSelected(null)}
        >
          <GroupDetail
            group={selected}
            isAdmin={isAdmin}
            onChanged={refresh}
            onGone={() => {
              setSelected(null);
              refresh();
            }}
          />
          {isAdmin && (
            <div style={{ marginTop: 20 }}>
              <div className="muted" style={{ fontSize: 11, textTransform: "uppercase", letterSpacing: 0.5, marginBottom: 8 }}>
                Admin actions
              </div>
              <button
                type="button"
                className="btn btn--danger"
                onClick={async () => {
                  if (!confirm(`Delete "${selected.name}"? Members + service assignments are removed; the services themselves stay.`)) return;
                  try {
                    await api.deleteGroup(selected.id);
                    setSelected(null);
                    refresh();
                  } catch (e) {
                    alert(String((e as Error).message ?? e));
                  }
                }}
              >
                Delete group
              </button>
            </div>
          )}
        </EditDrawer>
      )}
    </div>
  );
}

function CreateGroupForm({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [description, setDescription] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Auto-suggest slug from name unless the user has typed one.
  useEffect(() => {
    if (slugTouched) return;
    setSlug(
      name
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "-")
        .replace(/^-+|-+$/g, "")
        .slice(0, 48),
    );
  }, [name, slugTouched]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.createGroup({ name, slug, description });
      onCreated();
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setBusy(false);
    }
  };
  return (
    // Rendered inside an EditDrawer body — just .form, no .card.
    <form onSubmit={submit} className="form">
      <div className="form__row">
        <label className="form__label">
          Name
          <input
            className="search__input"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Orders Team"
            required
            autoFocus
          />
        </label>
        <label className="form__label">
          Slug
          <input
            className="search__input"
            value={slug}
            onChange={(e) => {
              setSlugTouched(true);
              setSlug(e.target.value);
            }}
            pattern="[a-z0-9-]+"
            placeholder="orders-team"
            required
          />
          <span className="form__hint">Lowercase letters, digits, hyphens.</span>
        </label>
      </div>
      <label className="form__label">
        Description
        <input
          className="search__input"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="What this group owns / which team is behind it"
        />
      </label>
      {error && <div className="alert alert--error">{error}</div>}
      <div className="form__actions">
        <button type="button" className="btn" onClick={onClose} disabled={busy}>
          Cancel
        </button>
        <button type="submit" className="btn btn--primary" disabled={busy || !name || !slug}>
          {busy ? "Creating…" : "Create group"}
        </button>
      </div>
    </form>
  );
}

function GroupDetail({ group, isAdmin, onChanged, onGone }: { group: Group; isAdmin: boolean; onChanged: () => void; onGone: () => void }) {
  const [members, setMembers] = useState<GroupMember[] | null>(null);
  const [orgMembers, setOrgMembers] = useState<MemberRow[] | null>(null);
  const [orgSAs, setOrgSAs] = useState<ServiceAccount[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);

  const refresh = () => {
    Promise.all([
      api.listGroupMembers(group.id),
      api.listMembers(),
      // Service accounts join groups too (that's how scoped SAs gain
      // visibility). The SA list is admin-only; non-admins still see SA
      // members rendered from the membership list itself.
      isAdmin ? api.listServiceAccounts().then((r) => r.service_accounts).catch(() => []) : Promise.resolve([]),
    ])
      .then(([gm, om, sas]) => {
        setMembers(gm.members);
        setOrgMembers(om.members);
        setOrgSAs(sas);
      })
      .catch((e) => {
        const msg = String((e as Error).message ?? e);
        // The group can vanish between list and click (deleted in another
        // tab / by a test run). Close the panel and refresh the list
        // instead of stranding the user on a dead error card.
        if (msg.startsWith("404")) {
          onGone();
          return;
        }
        setError(msg);
      });
  };
  // eslint-disable-next-line react-hooks/exhaustive-deps
  useEffect(refresh, [group.id]);

  if (error)
    return <div className="alert alert--error">Failed to load: {error}</div>;
  if (!members || !orgMembers) return <div className="placeholder">Loading members…</div>;

  // Org members / service accounts not already in this group — candidates to add.
  const inGroup = new Set(members.filter((m) => m.user).map((m) => m.user!.id));
  const saInGroup = new Set(members.filter((m) => m.service_account).map((m) => m.service_account!.id));
  const addable = orgMembers.filter((m) => !inGroup.has(m.user.id));
  const addableSAs = orgSAs.filter((sa) => !saInGroup.has(sa.id));

  // Rendered inside an EditDrawer (the blade owns the frame + title), so
  // no card chrome and no duplicated name/slug here.
  return (
    <div>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", marginBottom: 8 }}>
        <div>
          {group.description && (
            <div className="muted" style={{ fontSize: 12 }}>{group.description}</div>
          )}
        </div>
        {isAdmin && (
          <button
            type="button"
            className="btn btn--primary"
            onClick={() => setAdding(true)}
            disabled={(addable.length === 0 && addableSAs.length === 0) || adding}
          >
            + Add member
          </button>
        )}
      </div>

      {adding && (
        <EditDrawer
          title={`Add member to ${group.name}`}
          width="narrow"
          onClose={() => setAdding(false)}
        >
          <AddGroupMemberForm
            groupId={group.id}
            candidates={addable}
            saCandidates={addableSAs}
            onClose={() => setAdding(false)}
            onAdded={() => {
              setAdding(false);
              refresh();
              onChanged();
            }}
          />
        </EditDrawer>
      )}

      {members.length === 0 ? (
        <div className="muted" style={{ fontSize: 13 }}>No members yet.</div>
      ) : (
        <table className="table">
          <thead>
            <tr>
              <th>Email</th>
              <th>Role</th>
              <th>Joined</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {members.map((m) => {
              const isSA = !!m.service_account;
              const memberId = isSA ? m.service_account!.id : m.user!.id;
              const label = isSA ? m.service_account!.name : m.user!.email;
              return (
                <tr key={memberId}>
                  <td className="mono">
                    {label}
                    {isSA && (
                      <span className="badge" style={{ marginLeft: 8 }} title="Machine identity — this group defines what the service account can see">
                        service account
                      </span>
                    )}
                  </td>
                  <td>
                    {isAdmin ? (
                      <select
                        className="toolbar__select"
                        value={m.role}
                        onChange={async (e) => {
                          try {
                            if (isSA) {
                              await api.updateGroupServiceAccountRole(group.id, memberId, e.target.value as AuthRole);
                            } else {
                              await api.updateGroupMemberRole(group.id, memberId, e.target.value as AuthRole);
                            }
                            refresh();
                          } catch (err) {
                            alert(String((err as Error).message ?? err));
                          }
                        }}
                      >
                        <option value="admin">admin</option>
                        <option value="editor">editor</option>
                        <option value="viewer">viewer</option>
                      </select>
                    ) : (
                      <span className="badge">{m.role}</span>
                    )}
                  </td>
                  <td>{new Date(m.joined_at).toLocaleDateString()}</td>
                  <td className="num">
                    {isAdmin && (
                      <button
                        type="button"
                        className="btn btn--link"
                        style={{ color: "var(--err-ink, #ef4444)" }}
                        onClick={async () => {
                          if (!confirm(`Remove ${label} from "${group.name}"?`)) return;
                          try {
                            if (isSA) {
                              await api.removeGroupServiceAccount(group.id, memberId);
                            } else {
                              await api.removeGroupMember(group.id, memberId);
                            }
                            refresh();
                            onChanged();
                          } catch (e) {
                            alert(String((e as Error).message ?? e));
                          }
                        }}
                      >
                        Remove
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}

      <PoliciesSection groupId={group.id} isAdmin={isAdmin} onChanged={onChanged} />

      <div style={{ marginTop: 16, borderTop: "1px solid var(--border)", paddingTop: 12 }}>
        <div style={{ fontWeight: 600, fontSize: 13 }}>Notification channels</div>
        <p className="muted" style={{ fontSize: 12, margin: "2px 0" }}>
          This team&rsquo;s own alert-delivery channels are managed on the{" "}
          <Link to="/alerts">Alerts → Notification channels</Link> tab, alongside the org-wide ones.
        </p>
      </div>
    </div>
  );
}

// PoliciesSection — the ABAC layer. Each policy is one of:
//   service        — this specific service
//   integration    — every service in an integration
//   attributes     — any data whose resource attributes match
//   compound       — integration OR service AND attribute filter
//   all_org        — everything in the org (wildcard)
// Composes OR across policies in the group; AND inside one policy's
// attribute_match keys.
function PoliciesSection({
  groupId,
  isAdmin,
  onChanged,
}: {
  groupId: string;
  isAdmin: boolean;
  onChanged: () => void;
}) {
  const [policies, setPolicies] = useState<AccessPolicy[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  // Policy CRUD is Enterprise (rbac_advanced) — the backend 402s writes
  // on unlicensed cells, so don't offer the button; upsell instead. The
  // read-only list stays (listing is open, and CE cells can carry policies
  // created while a license was active).
  const { status: lic } = useLicense();
  const rbacEntitled = lic?.features?.rbac_advanced ?? false;

  const refresh = () => {
    api
      .listGroupPolicies(groupId)
      .then((r) => setPolicies(r.policies))
      .catch((e) => setError(String((e as Error).message ?? e)));
  };
  useEffect(refresh, [groupId]);

  return (
    <div style={{ marginTop: 16, paddingTop: 16, borderTop: "1px solid var(--border)" }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "baseline", marginBottom: 8 }}>
        <div>
          <div style={{ fontWeight: 600, fontSize: 13, display: "flex", alignItems: "center", gap: 8 }}>
            Access policies <EnterpriseBadge />
          </div>
          <div className="muted" style={{ fontSize: 12 }}>
            What data this group can see. Multiple policies OR together;
            an empty list means "no access" (strict default).
          </div>
        </div>
        {isAdmin && rbacEntitled && (
          <button type="button" className="btn" onClick={() => setAdding(true)} disabled={adding}>
            + Add policy
          </button>
        )}
      </div>

      {!rbacEntitled && (
        <UpgradeNotice title="Access policies are a Sluicio Enterprise feature" expired={lic?.expired}>
          <p className="muted" style={{ margin: 0, fontSize: 13 }}>
            In the Community edition, grant visibility by attaching this group
            to integrations or systems (Group access on their detail pages).
            An Enterprise license unlocks fine-grained policies — per service,
            per signal, attribute matches, and boolean expressions.
          </p>
        </UpgradeNotice>
      )}

      {error && <div className="alert alert--error">{error}</div>}
      {!policies && <div className="placeholder">Loading…</div>}

      {adding && (
        <EditDrawer
          title="New access policy"
          width="medium"
          onClose={() => setAdding(false)}
        >
          <CreatePolicyForm
            groupId={groupId}
            onClose={() => setAdding(false)}
            onCreated={() => {
              setAdding(false);
              refresh();
              onChanged();
            }}
          />
        </EditDrawer>
      )}

      {policies && policies.length === 0 && !adding && (
        <div className="muted" style={{ fontSize: 12, padding: "8px 4px" }}>
          No policies yet — this group has access to nothing.
        </div>
      )}

      {policies && policies.length > 0 && (
        <table className="table">
          <thead>
            <tr>
              <th>Kind</th>
              <th>What it grants</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {policies.map((p) => (
              <tr key={p.id}>
                <td><span className="badge">{p.kind}</span></td>
                <td>{withSignals(p, describePolicy(p))}</td>
                <td className="num">
                  {isAdmin && (
                    <button
                      type="button"
                      className="btn btn--link"
                      style={{ color: "var(--err-ink, #ef4444)" }}
                      onClick={async () => {
                        if (!confirm("Delete this policy?")) return;
                        try {
                          await api.deleteGroupPolicy(groupId, p.id);
                          refresh();
                          onChanged();
                        } catch (e) {
                          alert(String((e as Error).message ?? e));
                        }
                      }}
                    >
                      Delete
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

// describePolicy renders a one-line human summary of a policy row.
function describePolicy(p: AccessPolicy): string {
  switch (p.kind) {
    case "all_org":
      return "Everything in the org (wildcard)";
    case "system":
      return p.target_system_kind
        ? `All ${p.target_system_kind} systems`
        : "All flagged systems";
    case "service":
      return `Service ${p.target_service_name}`;
    case "integration":
      return `All services in integration ${(p.target_integration_id ?? "").slice(0, 8)}…`;
    case "attributes": {
      const kv = Object.entries(p.attribute_match)
        .map(([k, v]) => `${k}=${v}`)
        .join(", ");
      return `Where ${kv || "(no attrs)"}`;
    }
    case "compound": {
      const target = p.target_service_name
        ? `service ${p.target_service_name}`
        : `integration ${(p.target_integration_id ?? "").slice(0, 8)}…`;
      const kv = Object.entries(p.attribute_match)
        .map(([k, v]) => `${k}=${v}`)
        .join(", ");
      return `${target} where ${kv}`;
    }
    case "expression":
      return p.conditions ? describeExpr(p.conditions) : "(empty expression)";
  }
}

// withSignals appends a policy's signal narrowing to its summary.
function withSignals(p: AccessPolicy, text: string): string {
  if (!p.signals || p.signals.length === 0) return text;
  return `${text} — ${p.signals.join(", ")} only`;
}

// describeExpr renders a policy expression tree as a compact human string,
// e.g. `(service prefix "a" OR service = "file-mover") AND NOT namespace = "x"`.
function describeExpr(e: PolicyExpr, depth = 0): string {
  if (e.op === "and" || e.op === "or") {
    const joined = (e.children ?? []).map((c) => describeExpr(c, depth + 1)).join(` ${e.op.toUpperCase()} `);
    return depth > 0 ? `(${joined})` : joined;
  }
  if (e.op === "not") {
    return `NOT ${describeExpr((e.children ?? [])[0] ?? {}, depth + 1)}`;
  }
  // Leaf.
  const subject = e.attr ? e.attr : "service";
  const opText: Record<string, string> = {
    equals: "=",
    not_equals: "≠",
    prefix: "prefix",
    suffix: "suffix",
    contains: "contains",
    regex: "matches",
    in: "in",
    exists: "exists",
    not_exists: "not set",
  };
  const op = opText[e.match ?? ""] ?? e.match ?? "?";
  if (e.match === "exists" || e.match === "not_exists") return `${subject} ${op}`;
  if (e.match === "in") return `${subject} in [${(e.values ?? []).join(", ")}]`;
  return `${subject} ${op} "${e.value ?? ""}"`;
}

// ── Expression policy tree editor ──────────────────────────────────────
//
// Recursive editor for a kind='expression' policy's boolean tree. Each
// node is either an operator (and/or/not with children) or a leaf (match
// one service-name or resource-attribute condition). Mirrors the backend
// PolicyExpr shape 1:1 so the state IS the request body.

const EXPR_MATCHES: { value: PolicyExprMatch; label: string; attrOnly?: boolean; noValue?: boolean; list?: boolean }[] = [
  { value: "equals", label: "equals" },
  { value: "not_equals", label: "not equals" },
  { value: "prefix", label: "starts with" },
  { value: "suffix", label: "ends with" },
  { value: "contains", label: "contains" },
  { value: "regex", label: "matches regex" },
  { value: "in", label: "is one of", list: true },
  { value: "exists", label: "exists", attrOnly: true, noValue: true },
  { value: "not_exists", label: "is not set", attrOnly: true, noValue: true },
];

function emptyLeaf(): PolicyExpr {
  return { match: "equals", value: "" };
}

function ExprNodeEditor({
  node,
  onChange,
  onRemove,
  depth,
}: {
  node: PolicyExpr;
  onChange: (next: PolicyExpr) => void;
  onRemove?: () => void;
  depth: number;
}) {
  const isOp = node.op === "and" || node.op === "or" || node.op === "not";
  const nodeType: "group" | "not" | "leaf" = node.op === "not" ? "not" : isOp ? "group" : "leaf";

  // Switching a node's type reshapes it, preserving what makes sense.
  const setType = (t: "group" | "not" | "leaf") => {
    if (t === "leaf") {
      onChange(emptyLeaf());
    } else if (t === "not") {
      const first = node.children?.[0] ?? (isOp ? emptyLeaf() : node);
      onChange({ op: "not", children: [isOp ? first : node] });
    } else {
      const kids = node.children ?? (isOp ? [] : [node]);
      onChange({ op: node.op === "or" ? "or" : "and", children: kids.length ? kids : [emptyLeaf()] });
    }
  };

  const rowStyle = {
    borderLeft: depth > 0 ? "2px solid var(--border)" : undefined,
    paddingLeft: depth > 0 ? 10 : 0,
    marginBottom: 6,
  } as const;

  if (nodeType === "leaf") {
    // A leaf is an attribute leaf once `attr` is defined (even ""), so the
    // key input shows immediately after picking "attribute".
    const attrLeaf = node.attr !== undefined;
    const matches = EXPR_MATCHES.filter((m) => !m.attrOnly || attrLeaf);
    const current = EXPR_MATCHES.find((m) => m.value === node.match);
    return (
      <div style={{ ...rowStyle, display: "flex", flexWrap: "wrap", gap: 6, alignItems: "center" }}>
        <select
          className="toolbar__select"
          value={attrLeaf ? "attr" : "service"}
          onChange={(e) =>
            onChange(
              e.target.value === "attr"
                ? { ...node, attr: node.attr || "" }
                : { ...node, attr: undefined },
            )
          }
        >
          <option value="service">service name</option>
          <option value="attr">attribute</option>
        </select>
        {attrLeaf && (
          <input
            className="search__input mono"
            style={{ width: 150 }}
            value={node.attr ?? ""}
            placeholder="attr key"
            onChange={(e) => onChange({ ...node, attr: e.target.value })}
          />
        )}
        <select
          className="toolbar__select"
          value={node.match}
          onChange={(e) => {
            const m = e.target.value as PolicyExprMatch;
            const meta = EXPR_MATCHES.find((x) => x.value === m);
            const next: PolicyExpr = { ...node, match: m };
            if (meta?.noValue) {
              delete next.value;
              delete next.values;
            } else if (meta?.list) {
              next.values = node.values ?? (node.value ? [node.value] : []);
              delete next.value;
            } else {
              next.value = node.value ?? (node.values?.[0] ?? "");
              delete next.values;
            }
            onChange(next);
          }}
        >
          {matches.map((m) => (
            <option key={m.value} value={m.value}>{m.label}</option>
          ))}
        </select>
        {!current?.noValue && !current?.list && (
          <input
            className="search__input mono"
            style={{ width: 150 }}
            value={node.value ?? ""}
            placeholder="value"
            onChange={(e) => onChange({ ...node, value: e.target.value })}
          />
        )}
        {current?.list && (
          <input
            className="search__input mono"
            style={{ width: 200 }}
            value={(node.values ?? []).join(", ")}
            placeholder="a, b, c"
            onChange={(e) =>
              onChange({ ...node, values: e.target.value.split(",").map((s) => s.trim()).filter(Boolean) })
            }
          />
        )}
        <TypeSwitch value={nodeType} onChange={setType} />
        {onRemove && (
          <button type="button" className="btn" onClick={onRemove} title="Remove condition">✕</button>
        )}
      </div>
    );
  }

  // Operator node (and / or / not).
  const children = node.children ?? [];
  const setChild = (i: number, next: PolicyExpr) => {
    const kids = [...children];
    kids[i] = next;
    onChange({ ...node, children: kids });
  };
  const removeChild = (i: number) => onChange({ ...node, children: children.filter((_, j) => j !== i) });

  return (
    <div style={rowStyle}>
      <div style={{ display: "flex", gap: 6, alignItems: "center", marginBottom: 6 }}>
        {nodeType === "not" ? (
          <span className="mono" style={{ fontWeight: 600, color: "var(--danger, #b91c1c)" }}>NOT</span>
        ) : (
          <select
            className="toolbar__select"
            value={node.op}
            onChange={(e) => onChange({ ...node, op: e.target.value as PolicyExprOp })}
          >
            <option value="and">ALL of (AND)</option>
            <option value="or">ANY of (OR)</option>
          </select>
        )}
        <TypeSwitch value={nodeType} onChange={setType} />
        {onRemove && (
          <button type="button" className="btn" onClick={onRemove} title="Remove group">✕</button>
        )}
      </div>
      {children.map((c, i) => (
        <ExprNodeEditor
          key={i}
          node={c}
          depth={depth + 1}
          onChange={(next) => setChild(i, next)}
          onRemove={nodeType === "not" ? undefined : () => removeChild(i)}
        />
      ))}
      {nodeType !== "not" && (
        <div style={{ display: "flex", gap: 6, marginTop: 2, marginLeft: depth > 0 ? 10 : 0 }}>
          <button type="button" className="btn" onClick={() => onChange({ ...node, children: [...children, emptyLeaf()] })}>
            + condition
          </button>
          <button
            type="button"
            className="btn"
            onClick={() => onChange({ ...node, children: [...children, { op: "and", children: [emptyLeaf()] }] })}
          >
            + group
          </button>
        </div>
      )}
    </div>
  );
}

// TypeSwitch flips a node between leaf / group / not without losing the
// rest of the tree.
function TypeSwitch({ value, onChange }: { value: "group" | "not" | "leaf"; onChange: (t: "group" | "not" | "leaf") => void }) {
  return (
    <select
      className="toolbar__select"
      value={value}
      onChange={(e) => onChange(e.target.value as "group" | "not" | "leaf")}
      title="Node type"
      style={{ fontSize: 11 }}
    >
      <option value="leaf">condition</option>
      <option value="group">group</option>
      <option value="not">not</option>
    </select>
  );
}

function CreatePolicyForm({
  groupId,
  onClose,
  onCreated,
}: {
  groupId: string;
  onClose: () => void;
  onCreated: () => void;
}) {
  const [kind, setKind] = useState<PolicyKind>("attributes");
  const [serviceName, setServiceName] = useState("");
  const [integrationID, setIntegrationID] = useState("");
  const [systemKind, setSystemKind] = useState("");
  const [attrPairs, setAttrPairs] = useState<{ k: string; v: string }[]>([{ k: "", v: "" }]);
  const [expr, setExpr] = useState<PolicyExpr>({ op: "and", children: [{ match: "prefix", value: "" }] });
  const ALL_SIGNALS = ["traces", "logs", "metrics", "messages"] as const;
  // Messages and traces are the same underlying data seen through two
  // lenses with different sensitivity: messages = the curated business
  // view; traces = the raw technical view (span attributes, payloads).
  // Label them so granting one without the other reads as intentional.
  const SIGNAL_LABELS: Record<(typeof ALL_SIGNALS)[number], { label: string; hint: string }> = {
    messages: { label: "Messages", hint: "business view" },
    traces: { label: "Traces", hint: "technical view of the same flows" },
    logs: { label: "Logs", hint: "" },
    metrics: { label: "Metrics", hint: "" },
  };
  const [signals, setSignals] = useState<Set<string>>(new Set(ALL_SIGNALS));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const wantsService = kind === "service" || kind === "compound";
  const wantsIntegration = kind === "integration" || kind === "compound";
  const wantsAttrs = kind === "attributes" || kind === "compound";
  const wantsSystem = kind === "system";
  const wantsExpr = kind === "expression";

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const attribute_match: Record<string, string> = {};
      for (const { k, v } of attrPairs) {
        const kk = k.trim();
        const vv = v.trim();
        if (kk && vv) attribute_match[kk] = vv;
      }
      const body: AccessPolicyInput = {
        kind,
        target_service_name: wantsService ? serviceName.trim() : "",
        target_integration_id: wantsIntegration ? integrationID.trim() : "",
        target_system_kind: wantsSystem ? systemKind.trim() : "",
        attribute_match: wantsAttrs ? attribute_match : {},
        ...(wantsExpr ? { conditions: expr } : {}),
        ...(signals.size < 4 ? { signals: [...signals] as AccessPolicyInput["signals"] } : {}),
      };
      await api.createGroupPolicy(groupId, body);
      onCreated();
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setBusy(false);
    }
  };

  return (
    // Rendered inside an EditDrawer body — drawer supplies the surface.
    <form onSubmit={submit} className="form">
      <div className="form__row">
        <label className="form__label">
          Kind
          <select className="toolbar__select" value={kind} onChange={(e) => setKind(e.target.value as PolicyKind)}>
            <option value="service">service — pick one service explicitly</option>
            <option value="integration">integration — all services in an integration</option>
            <option value="attributes">attributes — match resource attributes</option>
            <option value="compound">compound — integration / service AND attributes</option>
            <option value="system">system — all flagged systems (optionally one kind)</option>
            <option value="all_org">all_org — wildcard, everything in the org</option>
            <option value="expression">expression — any AND/OR/NOT rule</option>
          </select>
        </label>
      </div>
      {wantsSystem && (
        <label className="form__label">
          System kind
          <select className="toolbar__select" value={systemKind} onChange={(e) => setSystemKind(e.target.value)}>
            <option value="">All systems</option>
            {SYSTEM_KINDS.map((k) => (
              <option key={k.value} value={k.value}>{k.label}</option>
            ))}
          </select>
          <span className="form__hint">
            Grants every service flagged as a system, or only those of one kind.
          </span>
        </label>
      )}
      {wantsService && (
        <label className="form__label">
          Service name
          <input
            className="search__input mono"
            value={serviceName}
            onChange={(e) => setServiceName(e.target.value)}
            placeholder="OrderService"
            required
          />
        </label>
      )}
      {wantsIntegration && (
        <label className="form__label">
          Integration UUID
          <input
            className="search__input mono"
            value={integrationID}
            onChange={(e) => setIntegrationID(e.target.value)}
            placeholder="00000000-0000-0000-0000-000000000000"
            required
          />
          <span className="form__hint">
            Copy from <code>/integrations</code> — a picker UI lands in a
            follow-up.
          </span>
        </label>
      )}
      {wantsAttrs && (
        <div className="form__label">
          <span>Resource-attribute filter (AND across all rows)</span>
          {attrPairs.map((p, i) => (
            <div key={i} style={{ display: "flex", gap: 8, marginBottom: 4 }}>
              <input
                className="search__input mono"
                style={{ flex: 1 }}
                value={p.k}
                placeholder="key (e.g. environment)"
                onChange={(e) => {
                  const next = [...attrPairs];
                  next[i] = { ...next[i], k: e.target.value };
                  setAttrPairs(next);
                }}
              />
              <input
                className="search__input mono"
                style={{ flex: 1 }}
                value={p.v}
                placeholder="value (e.g. prod)"
                onChange={(e) => {
                  const next = [...attrPairs];
                  next[i] = { ...next[i], v: e.target.value };
                  setAttrPairs(next);
                }}
              />
              <button
                type="button"
                className="btn"
                onClick={() => setAttrPairs(attrPairs.filter((_, j) => j !== i))}
              >
                ✕
              </button>
            </div>
          ))}
          <button type="button" className="btn" onClick={() => setAttrPairs([...attrPairs, { k: "", v: "" }])}>
            + Add attribute
          </button>
          <span className="form__hint">
            Sluicio records the resource attributes each service emits
            in recent telemetry; a policy here matches any service that
            currently carries every key/value pair listed.
          </span>
        </div>
      )}
      {wantsExpr && (
        <div className="form__label">
          <span>Expression</span>
          <div
            style={{
              border: "1px solid var(--border)",
              borderRadius: 6,
              padding: 10,
              background: "var(--surface-2)",
            }}
          >
            <ExprNodeEditor node={expr} depth={0} onChange={setExpr} />
          </div>
          <span className="form__hint" style={{ marginTop: 6 }}>
            Grants services matching this rule. Leaves match a service name
            or a resource attribute; combine with ALL / ANY / NOT groups.
            <br />
            Preview: <span className="mono">{describeExpr(expr)}</span>
          </span>
        </div>
      )}
      {kind !== "all_org" && (
        <div className="form__label">
          <span>Signals</span>
          <div style={{ display: "flex", gap: 14, flexWrap: "wrap" }}>
            {ALL_SIGNALS.map((sig) => (
              <label key={sig} style={{ display: "flex", alignItems: "center", gap: 6, fontSize: 13, cursor: "pointer" }}>
                <input
                  type="checkbox"
                  checked={signals.has(sig)}
                  onChange={(e) => {
                    const next = new Set(signals);
                    if (e.target.checked) next.add(sig);
                    else next.delete(sig);
                    setSignals(next);
                  }}
                />
                {SIGNAL_LABELS[sig].label}
                {SIGNAL_LABELS[sig].hint && (
                  <span className="muted" style={{ fontSize: 11 }}>· {SIGNAL_LABELS[sig].hint}</span>
                )}
              </label>
            ))}
          </div>
          <span className="form__hint">
            Which telemetry this policy grants. All four = full visibility;
            narrowing to a subset means members see the services but only the
            selected signals — and a signal-narrowed policy never grants
            manage rights, even for group editors. Messages and traces are
            the same flows at different depth: grant only Messages to give a
            business team the message list without the raw span data
            underneath.
          </span>
        </div>
      )}
      {error && <div className="alert alert--error">{error}</div>}
      <div className="form__actions">
        <button type="button" className="btn" onClick={onClose} disabled={busy}>Cancel</button>
        <button type="submit" className="btn btn--primary" disabled={busy || signals.size === 0}>
          {busy ? "Adding…" : "Add policy"}
        </button>
      </div>
    </form>
  );
}

function AddGroupMemberForm({
  groupId,
  candidates,
  saCandidates,
  onClose,
  onAdded,
}: {
  groupId: string;
  candidates: MemberRow[];
  saCandidates: ServiceAccount[];
  onClose: () => void;
  onAdded: () => void;
}) {
  // One picker for both principal kinds; values are prefixed so a user
  // id can never be mistaken for a service-account id. The optgroup
  // labels + the ⚙ marker keep service accounts visibly distinct.
  const [memberKey, setMemberKey] = useState(
    candidates[0] ? `u:${candidates[0].user.id}` : saCandidates[0] ? `sa:${saCandidates[0].id}` : "",
  );
  const [role, setRole] = useState<AuthRole>("editor");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isSA = memberKey.startsWith("sa:");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const id = memberKey.slice(memberKey.indexOf(":") + 1);
      await api.addGroupMember(groupId, isSA ? { service_account_id: id, role } : { user_id: id, role });
      onAdded();
    } catch (e) {
      setError(String((e as Error).message ?? e));
      setBusy(false);
    }
  };
  return (
    // Rendered inside an EditDrawer body — drawer supplies the surface.
    <form onSubmit={submit} className="form">
      <div className="form__row">
        <label className="form__label">
          Org member
          <select
            className="toolbar__select"
            value={memberKey}
            onChange={(e) => setMemberKey(e.target.value)}
            required
          >
            {candidates.length > 0 && (
              <optgroup label="Members">
                {candidates.map((m) => (
                  <option key={m.user.id} value={`u:${m.user.id}`}>
                    {m.user.email}
                  </option>
                ))}
              </optgroup>
            )}
            {saCandidates.length > 0 && (
              <optgroup label="Service accounts (machine identities)">
                {saCandidates.map((sa) => (
                  <option key={sa.id} value={`sa:${sa.id}`}>
                    ⚙ {sa.name} — service account
                  </option>
                ))}
              </optgroup>
            )}
          </select>
          <span className="form__hint">
            {isSA
              ? "A service account is a machine identity — membership here defines what its tokens can see."
              : "Only org members not already in this group are listed."}
          </span>
        </label>
        <label className="form__label">
          Role in group
          <select
            className="toolbar__select"
            value={role}
            onChange={(e) => setRole(e.target.value as AuthRole)}
          >
            <option value="admin">admin</option>
            <option value="editor">editor</option>
            <option value="viewer">viewer</option>
          </select>
        </label>
      </div>
      {error && <div className="alert alert--error">{error}</div>}
      <div className="form__actions">
        <button type="button" className="btn" onClick={onClose} disabled={busy}>
          Cancel
        </button>
        <button type="submit" className="btn btn--primary" disabled={busy || !memberKey}>
          {busy ? "Adding…" : "Add to group"}
        </button>
      </div>
    </form>
  );
}

// ── Retention tab ─────────────────────────────────────────────────────
//
// Cell-wide telemetry retention: how long traces / logs / metrics are
// kept before ClickHouse's TTL drops them. Three independent inputs;
// each saves independently so flipping metrics to 14 months without
// changing the others is one round-trip.
//
// Input UX: a number + a unit selector. Internally we always store
// days (matching the backend's bound check + ClickHouse's partition
// granularity), but presenting days for 14 months would be silly. The
// unit selector converts to/from days on render + save.

type RetentionUnit = "days" | "weeks" | "months" | "years";

const UNIT_DAYS: Record<RetentionUnit, number> = {
  days: 1,
  weeks: 7,
  // Approximate but consistent — 30 days per month, 365 per year.
  // Storage is days under the hood; the unit selector is purely a
  // friendlier surface for the same int.
  months: 30,
  years: 365,
};

function bestUnit(days: number): RetentionUnit {
  if (days >= 365 && days % 365 === 0) return "years";
  if (days >= 30 && days % 30 === 0) return "months";
  if (days >= 7 && days % 7 === 0) return "weeks";
  return "days";
}

function RetentionTab() {
  const { can } = useCurrentUser();
  const isAdmin = can("org.manage");
  const [data, setData] = useState<RetentionResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  const refresh = () => {
    api
      .getRetention()
      .then(setData)
      .catch((e) => setError(String((e as Error).message ?? e)));
  };
  useEffect(refresh, []);

  if (error) return <div className="alert alert--error">Failed to load: {error}</div>;
  if (!data) return <div className="placeholder">Loading…</div>;

  return (
    <div style={{ maxWidth: 720 }}>
      <p className="muted" style={{ fontSize: 13, marginBottom: 18, lineHeight: 1.55 }}>
        How long Sluicio keeps each kind of telemetry before ClickHouse
        evicts it. Settings apply <strong>cell-wide</strong> — every
        organization on this Sluicio install shares the same retention.
        The free tier caps retention at <strong>2 weeks</strong>; Sluicio
        Enterprise unlocks long retention (e.g. metrics raised to 14 months
        for capacity planning, traces and logs kept shorter for cost).
      </p>

      {data.apply_warning && (
        <div className="alert alert--warn" style={{ marginBottom: 16 }}>
          {data.apply_warning}
        </div>
      )}

      {data.long_retention === false && (
        <div
          className="card"
          style={{
            padding: "12px 14px",
            marginBottom: 16,
            display: "flex",
            alignItems: "center",
            gap: 10,
            borderColor: "color-mix(in oklab, var(--accent, #4c9aff) 35%, var(--border))",
          }}
        >
          <EnterpriseBadge />
          <span className="muted" style={{ fontSize: 13 }}>
            Free tier caps retention at <strong>{data.max_days} days</strong>. Sluicio
            Enterprise unlocks long retention — set a license key to raise the limit.
          </span>
        </div>
      )}

      <RetentionRow
        label="Traces"
        sublabel="OTLP spans — the parent/child waterfall of each message."
        days={data.traces.days}
        lastApplied={data.traces.last_applied_at}
        min={data.min_days}
        max={data.max_days}
        isAdmin={isAdmin}
        onSave={async (next) => {
          const r = await api.updateRetention({ traces_days: next });
          setData(r);
        }}
      />
      <RetentionRow
        label="Logs"
        sublabel="OTLP log records — search results, severity-banded volume."
        days={data.logs.days}
        lastApplied={data.logs.last_applied_at}
        min={data.min_days}
        max={data.max_days}
        isAdmin={isAdmin}
        onSave={async (next) => {
          const r = await api.updateRetention({ logs_days: next });
          setData(r);
        }}
      />
      <RetentionRow
        label="Metrics"
        sublabel="OTLP metric points — gauges, counters, histograms."
        days={data.metrics.days}
        lastApplied={data.metrics.last_applied_at}
        min={data.min_days}
        max={data.max_days}
        isAdmin={isAdmin}
        onSave={async (next) => {
          const r = await api.updateRetention({ metrics_days: next });
          setData(r);
        }}
      />
      <RetentionRow
        label="Audit log"
        sublabel={
          data.audit_configurable
            ? "Admin/security actions — pruned chain-safely; verification stays valid."
            : "Admin/security actions. Enterprise unlocks retention beyond the free cap."
        }
        days={data.audit_days}
        min={1}
        max={data.audit_max_days}
        isAdmin={isAdmin}
        onSave={async (next) => {
          const r = await api.updateRetention({ audit_days: next });
          setData(r);
        }}
      />

      <p className="muted" style={{ fontSize: 12, marginTop: 18, lineHeight: 1.5 }}>
        How it works: telemetry changes are written to Postgres, then pushed
        into ClickHouse's table TTL via an <code>ALTER TABLE … MODIFY
        TTL</code> statement. ClickHouse drops expired parts during
        its next merge cycle — minutes to hours, not instant. The
        background enforcer re-applies hourly as a safety net. Audit-log
        retention prunes Postgres rows directly on the same hourly cycle,
        preserving the tamper-evidence chain across the cut.
      </p>
    </div>
  );
}

// SystemSettingsTab — cell-wide system knobs. Today: the environment
// label shown in the top nav. Read is open; saving is admin-only (server
// enforces too). One Sluicio instance serves one org/environment, so the
// org admin owns this (issue #27).
// ── Reports tab ────────────────────────────────────────────────────────
//
// "Unused metrics" report: every metric Sluicio is ingesting + storing
// that no health check / alert references (rule_count === 0). These are
// the prime candidates to trim from ingestion. Reuses the metric catalog
// (which already carries per-metric rule counts), so it's read-only and
// adds no new endpoint.
const REPORT_RANGES = [
  { value: "1h", label: "Last hour" },
  { value: "24h", label: "Last 24h" },
  { value: "7d", label: "Last 7 days" },
  { value: "30d", label: "Last 30 days" },
];

// PAGE is how many unused-metric rows the Reports tab renders per chunk; more
// load in as the table is scrolled (the catalog can be thousands of metrics).
const REPORTS_PAGE = 60;

function ReportsTab() {
  const [range, setRange] = useState("24h");
  const [metrics, setMetrics] = useState<MetricCatalogEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [trimOpen, setTrimOpen] = useState(false);
  const [renderCount, setRenderCount] = useState(REPORTS_PAGE);

  useEffect(() => {
    setLoading(true);
    setError(null);
    setRenderCount(REPORTS_PAGE);
    api
      .metricCatalog(range)
      .then((r) => setMetrics(r.metrics ?? []))
      .catch((e) => setError(String(e.message ?? e)))
      .finally(() => setLoading(false));
  }, [range]);

  const unused = useMemo(
    () =>
      metrics
        .filter((m) => (m.rule_count ?? 0) === 0)
        .sort((a, b) => (b.series_count ?? 0) - (a.series_count ?? 0)),
    [metrics],
  );
  const shown = unused.slice(0, renderCount);

  // Grow the rendered window when the user scrolls near the bottom.
  const onScroll = (e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 200) {
      setRenderCount((c) => Math.min(unused.length, c + REPORTS_PAGE));
    }
  };

  return (
    <div>
      <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 12, flexWrap: "wrap", marginBottom: 8 }}>
        <h2 style={{ margin: 0 }}>Metrics not used in health checks</h2>
        <div style={{ display: "flex", gap: 8 }}>
          <button type="button" className="btn" onClick={() => setTrimOpen(true)}>✂ Trim ingestion</button>
          <select className="toolbar__select" value={range} onChange={(e) => setRange(e.target.value)}>
            {REPORT_RANGES.map((r) => <option key={r.value} value={r.value}>{r.label}</option>)}
          </select>
        </div>
      </div>
      <p className="muted" style={{ fontSize: 13.5, marginTop: 0 }}>
        These metrics are being ingested and stored, but no health check or alert rule references them — the prime
        candidates to trim. Open{" "}
        <button type="button" className="btn btn--link" style={{ padding: 0 }} onClick={() => setTrimOpen(true)}>
          Trim ingestion
        </button>{" "}
        to generate a collector config that drops the ones you don't need.
      </p>

      {error && <div className="alert alert--error">{error}</div>}

      {loading ? (
        <div className="placeholder">Loading…</div>
      ) : metrics.length === 0 ? (
        <div className="placeholder">No metrics seen in this window.</div>
      ) : unused.length === 0 ? (
        <div className="placeholder">Every metric in this window is watched by a health check. Nothing to trim. 🟢</div>
      ) : (
        <>
          <div className="muted" style={{ fontSize: 13, margin: "6px 0 10px" }}>
            <strong style={{ color: "var(--ink)" }}>{unused.length}</strong> of {metrics.length} metrics are unused
            (highest series count first).
          </div>
          <div
            style={{ maxHeight: 460, overflow: "auto", border: "1px solid var(--border)", borderRadius: 6 }}
            onScroll={onScroll}
          >
            <table className="table">
              <thead>
                <tr>
                  <th>Metric</th>
                  <th>Type</th>
                  <th className="num">Series</th>
                  <th className="num">Points</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((m) => (
                  <tr key={m.name}>
                    <td className="mono" style={{ fontSize: 12.5 }}>{m.name}</td>
                    <td className="muted">{m.type}</td>
                    <td className="num">{(m.series_count ?? 0).toLocaleString()}</td>
                    <td className="num">{(m.point_count ?? 0).toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>
            Showing {shown.length} of {unused.length}
            {shown.length < unused.length ? " — scroll for more" : ""}
          </div>
        </>
      )}

      {trimOpen && <TrimIngestionPanel window={range} onClose={() => setTrimOpen(false)} />}
    </div>
  );
}

function SystemSettingsTab() {
  const { can } = useCurrentUser();
  const isAdmin = can("org.manage");
  const [data, setData] = useState<SystemSettings | null>(null);
  const [env, setEnv] = useState("");
  const [ingest, setIngest] = useState("");
  const [map5xx, setMap5xx] = useState(false);
  const [forbidOrgWideSAs, setForbidOrgWideSAs] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [savedAt, setSavedAt] = useState(0);

  useEffect(() => {
    api
      .getSystemSettings()
      .then((d) => {
        setData(d);
        setEnv(d.environment);
        setIngest(d.ingest_base_url ?? "");
        setMap5xx(Boolean(d.map_http_5xx_to_error));
        setForbidOrgWideSAs(Boolean(d.forbid_org_wide_service_accounts));
      })
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, []);

  if (error) return <div className="alert alert--error">Failed to load: {error}</div>;
  if (!data) return <div className="placeholder">Loading…</div>;

  const envManaged = data.ingest_url_source === "env";
  const envDirty = env.trim() !== data.environment && env.trim() !== "";
  const ingestDirty = !envManaged && ingest.trim() !== (data.ingest_base_url ?? "");
  const map5xxDirty = map5xx !== Boolean(data.map_http_5xx_to_error);
  const forbidSAsDirty = forbidOrgWideSAs !== Boolean(data.forbid_org_wide_service_accounts);
  const dirty = envDirty || ingestDirty || map5xxDirty || forbidSAsDirty;

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      // env can't be blank (backend rejects it); always send the current
      // value. ingest_base_url may be cleared to "" to revert to the
      // browser-origin default.
      const r = await api.updateSystemSettings({
        environment: env.trim() || data.environment,
        // Never send an env-managed ingest URL — the server refuses it.
        ...(envManaged ? {} : { ingest_base_url: ingest.trim() }),
        map_http_5xx_to_error: map5xx,
        forbid_org_wide_service_accounts: forbidOrgWideSAs,
      });
      setData(r);
      setEnv(r.environment);
      setIngest(r.ingest_base_url);
      setMap5xx(Boolean(r.map_http_5xx_to_error));
      setForbidOrgWideSAs(Boolean(r.forbid_org_wide_service_accounts));
      setSavedAt(Date.now());
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  return (
    <div style={{ maxWidth: 720 }}>
      <p className="muted" style={{ fontSize: 13, marginBottom: 18, lineHeight: 1.55 }}>
        Cell-wide system settings. The <strong>environment</strong> label
        is shown in the top navigation (e.g. <code>production</code>,{" "}
        <code>staging</code>). The <strong>ingest base URL</strong> is the
        public OTLP/HTTP address your collectors reach this cell at — it's
        baked into the ready-to-paste exporter config on the Ingestion tab.
      </p>

      <form onSubmit={save} style={{ display: "flex", flexDirection: "column", gap: 14, maxWidth: 480 }}>
        <label className="form__label">
          Environment
          <input
            className="search__input"
            value={env}
            maxLength={40}
            disabled={!isAdmin}
            onChange={(e) => setEnv(e.target.value)}
            placeholder="production"
          />
        </label>
        <label className="form__label">
          Ingest base URL
          <input
            className="search__input"
            value={ingest}
            maxLength={200}
            disabled={!isAdmin || envManaged}
            onChange={(e) => setIngest(e.target.value)}
            placeholder={window.location.origin}
          />
          {envManaged ? (
            <span className="form__hint" style={{ fontSize: 12 }}>
              Managed by the deployment (<code>SLUICIO_INGEST_URL</code>) — change it
              in the server environment, not here. That keeps the endpoint correct
              by construction instead of by memory.
            </span>
          ) : (
            <span className="form__hint" style={{ fontSize: 12 }}>
              The external OTLP/HTTP endpoint of this cell's ingest (cell-ingest),
              e.g. <code>https://ingest.acme.example.com</code>. Prefer setting{" "}
              <code>SLUICIO_INGEST_URL</code> in the deployment environment; this
              field is the fallback for hand-rolled hosts. Leave blank to use
              the host you open the app on (<code>{window.location.origin}</code>) —
              fine for single-host deployments. The OpenTelemetry SDK appends{" "}
              <code>/v1/traces</code> etc. automatically, so omit the path.
            </span>
          )}
        </label>
        <label className="form__label" style={{ flexDirection: "row", alignItems: "flex-start", gap: 10 }}>
          <input
            type="checkbox"
            checked={map5xx}
            disabled={!isAdmin}
            onChange={(e) => setMap5xx(e.target.checked)}
            style={{ marginTop: 3 }}
          />
          <span>
            Treat HTTP 5xx as errors
            <span className="form__hint" style={{ display: "block", fontSize: 12, fontWeight: 400 }}>
              Some emitters — API gateways like KrakenD, notably — record a
              5xx only as the <code>http.response.status_code</code> attribute
              and leave the span status OK, making failures invisible to health,
              error counts, and failed-trace alert rules. When enabled, this cell
              stores such spans as error spans at ingest (stamped{" "}
              <code>sluicio.status_mapped</code>). Applies to newly ingested
              spans only; takes effect within ~30 seconds.
            </span>
          </span>
        </label>
        <label className="form__label" style={{ flexDirection: "row", alignItems: "flex-start", gap: 10 }}>
          <input
            type="checkbox"
            checked={forbidOrgWideSAs}
            disabled={!isAdmin}
            onChange={(e) => setForbidOrgWideSAs(e.target.checked)}
            style={{ marginTop: 3 }}
          />
          <span>
            Disallow org-wide service accounts
            <span className="form__hint" style={{ display: "block", fontSize: 12, fontWeight: 400 }}>
              Compliance posture: every service account must be <strong>scoped</strong> —
              its tokens see only what its group memberships grant, exactly like a
              user. Creating or switching an account to org-wide read is rejected,
              and any existing org-wide account resolves as scoped while this is
              enabled. Admin-role accounts are unaffected (admin is admin).
            </span>
          </span>
        </label>
        {isAdmin ? (
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <button className="btn btn--primary" type="submit" disabled={saving || !dirty}>
              {saving ? "Saving…" : "Save"}
            </button>
            {savedAt > 0 && !dirty && (
              <span className="muted" style={{ fontSize: 12 }}>Saved.</span>
            )}
          </div>
        ) : (
          <p className="muted" style={{ fontSize: 12 }}>
            Only an organization admin can change these settings.
          </p>
        )}
      </form>

      <SmtpSettings isAdmin={isAdmin} />
      <SecurityPolicy isAdmin={isAdmin} />
    </div>
  );
}

// SecurityPolicy — org-wide MFA enforcement (Enterprise). The toggle is
// disabled with an upsell when the mfa_policy entitlement isn't active.
function SecurityPolicy({ isAdmin }: { isAdmin: boolean }) {
  const [required, setRequired] = useState(false);
  const [entitled, setEntitled] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .getSecuritySettings()
      .then((s) => { setRequired(s.mfa_required); setEntitled(s.mfa_policy_entitled); setLoaded(true); })
      .catch((e) => setError(String((e as Error).message ?? e)));
  }, []);

  if (!loaded) return null;

  const toggle = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await api.updateSecuritySettings(!required);
      setRequired(r.mfa_required);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div style={{ marginTop: 28, borderTop: "1px solid var(--border)", paddingTop: 20 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 4 }}>
        <h3 style={{ fontSize: 14, fontWeight: 600, margin: 0 }}>Security policy</h3>
        <EnterpriseBadge />
      </div>
      <p className="muted" style={{ fontSize: 13, lineHeight: 1.55, margin: "0 0 12px" }}>
        Require every member to set up two-factor authentication. Members
        without MFA are prompted to enrol before using Sluicio. (SSO users get
        MFA from their identity provider.)
      </p>
      {error && <div className="alert alert--error" style={{ marginBottom: 10 }}>{error}</div>}
      {entitled ? (
        <label style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 13.5 }}>
          <input type="checkbox" checked={required} disabled={!isAdmin || busy} onChange={toggle} />
          Require two-factor authentication for all members
        </label>
      ) : (
        <div className="muted" style={{ fontSize: 13 }}>
          Org-wide MFA enforcement is a Sluicio Enterprise feature — set a license key to enable it.
          (Individual users can still turn on 2FA for themselves under Account → Two-factor.)
        </div>
      )}
    </div>
  );
}

// SmtpSettings — global transactional-email transport (password resets,
// account email). Admin-only; the password is write-only (never returned).
function SmtpSettings({ isAdmin }: { isAdmin: boolean }) {
  const [data, setData] = useState<SMTPSettingsResponse | null>(null);
  const [form, setForm] = useState({ host: "", port: "", username: "", password: "", from: "", from_name: "" });
  const [pwTouched, setPwTouched] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [savedAt, setSavedAt] = useState(0);
  const [testTo, setTestTo] = useState("");
  const [testMsg, setTestMsg] = useState<string | null>(null);

  const load = () => {
    api
      .getSMTP()
      .then((d) => {
        setData(d);
        setForm({ host: d.host, port: d.port, username: d.username, password: "", from: d.from, from_name: d.from_name });
        setPwTouched(false);
      })
      .catch((e) => setError(String((e as Error).message ?? e)));
  };
  useEffect(load, []);

  if (error) return <div className="alert alert--error" style={{ marginTop: 24 }}>SMTP: {error}</div>;
  if (!data) return null;

  const set = (k: keyof typeof form) => (e: { target: { value: string } }) =>
    setForm((f) => ({ ...f, [k]: e.target.value }));

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError(null);
    try {
      const body = {
        host: form.host,
        port: form.port,
        username: form.username,
        from: form.from,
        from_name: form.from_name,
        // Only send password when the admin actually typed one.
        ...(pwTouched ? { password: form.password } : {}),
      };
      const r = await api.updateSMTP(body);
      setData(r);
      setForm((f) => ({ ...f, password: "" }));
      setPwTouched(false);
      setSavedAt(Date.now());
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setSaving(false);
    }
  };

  const sendTest = async () => {
    setTestMsg(null);
    try {
      const r = await api.testSMTP(testTo.trim() || undefined);
      setTestMsg(`✓ Test email sent to ${r.to}.`);
    } catch (e) {
      setTestMsg(`✗ ${String((e as Error).message ?? e)}`);
    }
  };

  return (
    <div style={{ marginTop: 28, borderTop: "1px solid var(--border)", paddingTop: 20 }}>
      <h3 style={{ fontSize: 14, fontWeight: 600, margin: "0 0 4px" }}>Email (SMTP)</h3>
      <p className="muted" style={{ fontSize: 13, lineHeight: 1.55, margin: "0 0 14px" }}>
        The transport used for transactional email — password-reset links and
        future account notifications. Environment variables (<code>SLUICIO_SMTP_*</code>)
        act as defaults; anything set here overrides them.{" "}
        <strong style={{ color: data.configured ? "var(--ok, #3fb950)" : "var(--muted)" }}>
          {data.configured ? "Currently configured ✓" : "Not configured yet"}
        </strong>
      </p>

      <form onSubmit={save} style={{ display: "flex", flexDirection: "column", gap: 10, maxWidth: 420 }}>
        <div style={{ display: "flex", gap: 10 }}>
          <label className="form__label" style={{ flex: 2 }}>
            SMTP host
            <input className="search__input" value={form.host} disabled={!isAdmin} onChange={set("host")} placeholder="smtp.example.com" />
          </label>
          <label className="form__label" style={{ flex: 1 }}>
            Port
            <input className="search__input" value={form.port} disabled={!isAdmin} onChange={set("port")} placeholder="587" />
          </label>
        </div>
        <label className="form__label">
          Username
          <input className="search__input" value={form.username} disabled={!isAdmin} onChange={set("username")} placeholder="apikey / user@example.com" />
        </label>
        <label className="form__label">
          Password {data.password_set && <span className="muted" style={{ fontSize: 11 }}>(set — leave blank to keep)</span>}
          <input className="search__input" type="password" value={form.password} disabled={!isAdmin}
            onChange={(e) => { setForm((f) => ({ ...f, password: e.target.value })); setPwTouched(true); }}
            placeholder={data.password_set ? "••••••••" : ""} />
        </label>
        <div style={{ display: "flex", gap: 10 }}>
          <label className="form__label" style={{ flex: 1 }}>
            From address
            <input className="search__input" value={form.from} disabled={!isAdmin} onChange={set("from")} placeholder="noreply@example.com" />
          </label>
          <label className="form__label" style={{ flex: 1 }}>
            From name
            <input className="search__input" value={form.from_name} disabled={!isAdmin} onChange={set("from_name")} placeholder="Sluicio" />
          </label>
        </div>
        {isAdmin && (
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <button className="btn btn--primary" type="submit" disabled={saving}>
              {saving ? "Saving…" : "Save SMTP settings"}
            </button>
            {savedAt > 0 && <span className="muted" style={{ fontSize: 12 }}>Saved.</span>}
          </div>
        )}
      </form>

      {isAdmin && (
        <div style={{ marginTop: 16, display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
          <input className="search__input" style={{ maxWidth: 240 }} value={testTo}
            onChange={(e) => setTestTo(e.target.value)} placeholder="you@example.com (test recipient)" />
          <button className="btn" type="button" onClick={sendTest}>Send test email</button>
          {testMsg && <span className="muted" style={{ fontSize: 12.5 }}>{testMsg}</span>}
        </div>
      )}
    </div>
  );
}

function RetentionRow({
  label,
  sublabel,
  days,
  lastApplied,
  min,
  max,
  isAdmin,
  onSave,
}: {
  label: string;
  sublabel: string;
  days: number;
  lastApplied?: string;
  min: number;
  max: number;
  isAdmin: boolean;
  onSave: (days: number) => Promise<void>;
}) {
  const initialUnit = bestUnit(days);
  const [unit, setUnit] = useState<RetentionUnit>(initialUnit);
  const [n, setN] = useState<number>(Math.round(days / UNIT_DAYS[initialUnit]));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Re-sync when the parent reloads (e.g. after another row saved).
  useEffect(() => {
    const u = bestUnit(days);
    setUnit(u);
    setN(Math.round(days / UNIT_DAYS[u]));
  }, [days]);

  const proposedDays = n * UNIT_DAYS[unit];
  const dirty = proposedDays !== days;
  const valid = proposedDays >= min && proposedDays <= max && n > 0;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!valid || !dirty) return;
    setBusy(true);
    setError(null);
    try {
      await onSave(proposedDays);
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <form
      onSubmit={submit}
      style={{
        display: "grid",
        gridTemplateColumns: "1fr auto",
        gap: 14,
        padding: "14px 16px",
        marginBottom: 12,
        background: "var(--surface-2)",
        border: "1px solid var(--border)",
        borderRadius: 8,
        alignItems: "center",
      }}
    >
      <div style={{ minWidth: 0 }}>
        <div style={{ fontWeight: 600, fontSize: 14 }}>{label}</div>
        <div className="muted" style={{ fontSize: 12 }}>{sublabel}</div>
        {lastApplied && (
          <div className="muted" style={{ fontSize: 11, marginTop: 4 }}>
            Last applied to ClickHouse: {new Date(lastApplied).toLocaleString()}
          </div>
        )}
        {error && <div className="alert alert--error" style={{ marginTop: 8 }}>{error}</div>}
      </div>
      <div style={{ display: "flex", gap: 6, alignItems: "center" }}>
        <input
          type="number"
          className="search__input mono"
          min={1}
          value={n}
          disabled={!isAdmin || busy}
          onChange={(e) => setN(Math.max(0, parseInt(e.target.value, 10) || 0))}
          style={{ width: 80, textAlign: "right" }}
        />
        <select
          className="toolbar__select"
          value={unit}
          disabled={!isAdmin || busy}
          onChange={(e) => setUnit(e.target.value as RetentionUnit)}
        >
          <option value="days">days</option>
          <option value="weeks">weeks</option>
          <option value="months">months</option>
          <option value="years">years</option>
        </select>
        {isAdmin && (
          <button
            type="submit"
            className="btn btn--primary"
            disabled={!dirty || !valid || busy}
            style={{ marginLeft: 4 }}
          >
            {busy ? "Saving…" : "Save"}
          </button>
        )}
        {!valid && n > 0 && (
          <span className="muted" style={{ fontSize: 11, marginLeft: 4, color: "var(--err)" }}>
            {proposedDays < min ? `min ${min}d` : `max ${max}d`}
          </span>
        )}
      </div>
    </form>
  );
}

// ── SSO tab (placeholder until OIDC sign-in flow ships) ────────────────

function SsoTab() {
  const { status } = useLicense();
  const entitled = status?.features?.sso ?? false;
  return (
    <div style={{ maxWidth: 760 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 10 }}>
        <h2 style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>Single sign-on (OIDC)</h2>
        <EnterpriseBadge />
      </div>
      {!entitled ? (
        <UpgradeNotice title="SSO is a Sluicio Enterprise feature" expired={status?.expired}>
          <p className="muted" style={{ margin: 0, fontSize: 13 }}>
            Connect your identity provider (Entra, Okta, Google Workspace,
            Keycloak — anything OIDC-conformant), with IdP groups mapped to
            Sluicio roles and teams. Email + password login keeps working
            without a license.
          </p>
        </UpgradeNotice>
      ) : (
        <SsoSettings />
      )}
    </div>
  );
}

// ── Audit log tab (Enterprise) ─────────────────────────────────────────

const AUDIT_PAGE = 100;

// Common action prefixes, offered as datalist suggestions on the action
// filter. Free text still works — the server does prefix matching.
const AUDIT_ACTION_HINTS = [
  "login.",
  "session.",
  "password.",
  "mfa.",
  "member.",
  "token.",
  "group.",
  "group_member.",
  "group_policy.",
  "org.",
  "operator.",
  "integration.",
  "alert_rule.",
  "notification_channel.",
  "ingest_key.",
  "service_account.",
  "auth_provider.",
  "retention.",
  "smtp.",
];

function AuditLogTab() {
  const { status, loading: licLoading } = useLicense();
  const entitled = status?.features?.audit_log ?? false;
  const [entries, setEntries] = useState<AuditEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [expanded, setExpanded] = useState<number | null>(null);
  const [verify, setVerify] = useState<AuditVerifyResult | null>(null);
  const [verifying, setVerifying] = useState(false);

  const runVerify = () => {
    setVerifying(true);
    setVerify(null);
    api
      .verifyAuditChain()
      .then(setVerify)
      .catch((e) => setError(String((e as Error).message ?? e)))
      .finally(() => setVerifying(false));
  };
  // Filters. actor/action are live-debounced; from/to apply on change.
  const [actor, setActor] = useState("");
  const [action, setAction] = useState("");
  const [from, setFrom] = useState(""); // datetime-local strings
  const [to, setTo] = useState("");
  // actorId pins one user by their stable UUID — set by clicking an actor
  // in the table. Unlike the text filter it survives renames: entries
  // written under a previous name still match.
  const [actorId, setActorId] = useState<{ id: string; label: string } | null>(null);

  // A datetime-local value is in the browser's local zone; the API wants
  // RFC3339. `to` is exclusive server-side, so the picked minute is
  // included by sending it as-is (…T10:00 excludes 10:00:00 onwards).
  const toRFC3339 = (local: string): string | undefined =>
    local ? new Date(local).toISOString() : undefined;

  const filters = useMemo(
    () => ({
      actor: actor.trim() || undefined,
      actorId: actorId?.id,
      action: action.trim() || undefined,
      from: toRFC3339(from),
      to: toRFC3339(to),
    }),
    [actor, actorId, action, from, to],
  );

  useEffect(() => {
    if (!entitled) return;
    // Debounce so typing an email doesn't fire a query per keystroke.
    const t = setTimeout(() => {
      setError(null);
      api
        .listAuditLog({ limit: AUDIT_PAGE, ...filters })
        .then((r) => {
          const es = r.entries ?? [];
          setEntries(es);
          setHasMore(es.length === AUDIT_PAGE);
        })
        .catch((e) => setError(String((e as Error).message ?? e)));
    }, 300);
    return () => clearTimeout(t);
  }, [entitled, filters]);

  const loadMore = () => {
    if (!entries?.length || loadingMore || !hasMore) return;
    setLoadingMore(true);
    api
      .listAuditLog({ limit: AUDIT_PAGE, before: entries[entries.length - 1].id, ...filters })
      .then((r) => {
        const es = r.entries ?? [];
        setEntries([...entries, ...es]);
        setHasMore(es.length === AUDIT_PAGE);
      })
      .catch((e) => setError(String((e as Error).message ?? e)))
      .finally(() => setLoadingMore(false));
  };

  // Fetch the next keyset page when the user scrolls near the bottom —
  // same trigger the Reports tab uses, but against the server instead of
  // an in-memory slice.
  const onScroll = (e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    if (el.scrollHeight - el.scrollTop - el.clientHeight < 200) {
      loadMore();
    }
  };

  const anyFilter = Boolean(
    filters.actor || filters.actorId || filters.action || filters.from || filters.to,
  );

  // Export link carries the active filters so "what you see is what you
  // export". Session cookie rides along on the plain anchor.
  const exportHref = useMemo(() => {
    const p = new URLSearchParams({ format: "csv" });
    if (filters.actor) p.set("actor", filters.actor);
    if (filters.actorId) p.set("actor_id", filters.actorId);
    if (filters.action) p.set("action", filters.action);
    if (filters.from) p.set("from", filters.from);
    if (filters.to) p.set("to", filters.to);
    return `/api/v1/audit-log?${p.toString()}`;
  }, [filters]);

  if (licLoading) return <div className="placeholder">Loading…</div>;
  if (!entitled) {
    return (
      <div style={{ maxWidth: 640 }}>
        <UpgradeNotice title="Audit log is a Sluicio Enterprise feature" expired={status?.expired}>
          <p className="muted" style={{ margin: 0, fontSize: 13 }}>
            A tamper-evident record of who changed what — members, tokens, access
            policies, retention, SSO config — with actor, timestamp, and IP.
          </p>
        </UpgradeNotice>
      </div>
    );
  }

  return (
    <div>
      {/* The tab's page header already titles the screen "Audit log" —
          a second in-card heading duplicated it (and broke strict-mode
          heading locators). Keep just the ENT badge row. */}
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12 }}>
        <EnterpriseBadge />
      </div>

      {/* Filter bar: who / what / when. */}
      <div
        style={{ display: "flex", flexWrap: "wrap", alignItems: "flex-end", gap: 10, marginBottom: 14 }}
      >
        <label className="form__label" style={{ marginBottom: 0 }}>
          Actor
          <input
            type="text"
            className="search__input"
            placeholder="name or email"
            value={actor}
            onChange={(e) => setActor(e.target.value)}
            style={{ width: 190 }}
          />
        </label>
        <label className="form__label" style={{ marginBottom: 0 }}>
          Action
          <input
            type="text"
            className="search__input"
            placeholder={'prefix, e.g. "member."'}
            value={action}
            onChange={(e) => setAction(e.target.value)}
            list="audit-action-hints"
            style={{ width: 190 }}
          />
          <datalist id="audit-action-hints">
            {AUDIT_ACTION_HINTS.map((a) => (
              <option key={a} value={a} />
            ))}
          </datalist>
        </label>
        <label className="form__label" style={{ marginBottom: 0 }}>
          From
          <input
            type="datetime-local"
            className="search__input"
            value={from}
            onChange={(e) => setFrom(e.target.value)}
          />
        </label>
        <label className="form__label" style={{ marginBottom: 0 }}>
          To
          <input
            type="datetime-local"
            className="search__input"
            value={to}
            onChange={(e) => setTo(e.target.value)}
          />
        </label>
        {actorId && (
          <span
            className="mono"
            data-testid="audit-actor-chip"
            style={{
              display: "inline-flex",
              alignItems: "center",
              gap: 6,
              fontSize: 12,
              padding: "4px 8px",
              borderRadius: 999,
              border: "1px solid var(--border)",
              background: "var(--surface-2)",
              color: "var(--ink-2)",
            }}
            title={`Only entries by this user (id ${actorId.id}) — matches across renames`}
          >
            user: {actorId.label}
            <button
              type="button"
              aria-label="Stop filtering by this user"
              onClick={() => setActorId(null)}
              style={{ border: 0, background: "none", cursor: "pointer", color: "var(--muted)", padding: 0 }}
            >
              ✕
            </button>
          </span>
        )}
        {anyFilter && (
          <button
            type="button"
            className="btn"
            onClick={() => {
              setActor("");
              setAction("");
              setFrom("");
              setTo("");
              setActorId(null);
            }}
          >
            Clear
          </button>
        )}
        <a className="btn" href={exportHref} download title="Download the filtered entries as CSV">
          Export CSV
        </a>
        <button
          type="button"
          className="btn"
          onClick={runVerify}
          disabled={verifying}
          title="Re-derive every entry's hash chain and check nothing was altered or removed"
        >
          {verifying ? "Verifying…" : "Verify integrity"}
        </button>
      </div>

      {verify && (
        <div
          className={verify.ok ? "alert" : "alert alert--error"}
          style={{ marginBottom: 12 }}
          data-testid="audit-verify-result"
        >
          {verify.ok ? (
            <>
              Chain intact — {verify.entries_checked} entries verified
              {verify.legacy_unhashed > 0 &&
                ` (${verify.legacy_unhashed} pre-chain entries can't be verified retroactively)`}
              .
            </>
          ) : (
            <>
              Integrity check FAILED at entry #{verify.first_broken_id}: {verify.detail}. Entries
              before this point verified clean ({verify.entries_checked}). Treat this log as
              tampered and investigate database access.
            </>
          )}
        </div>
      )}

      {error && <div className="alert alert--error">Failed to load: {error}</div>}
      {!error && entries === null && <div className="placeholder">Loading…</div>}
      {entries !== null && entries.length === 0 && (
        <div className="placeholder">
          {anyFilter ? "No audit entries match these filters." : "No audited actions yet."}
        </div>
      )}
      {entries !== null && entries.length > 0 && (
        <div
          style={{ maxHeight: 560, overflow: "auto", border: "1px solid var(--border)", borderRadius: 6 }}
          onScroll={onScroll}
          data-testid="audit-scroll"
        >
          <table className="table" style={{ width: "100%", fontSize: 13 }}>
            <thead>
              <tr>
                <th style={{ textAlign: "left" }}>When</th>
                <th style={{ textAlign: "left" }}>Actor</th>
                <th style={{ textAlign: "left" }}>Action</th>
                <th style={{ textAlign: "left" }}>Target</th>
                <th style={{ textAlign: "left" }}>IP</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <Fragment key={e.id}>
                  <tr
                    onClick={() => setExpanded(expanded === e.id ? null : e.id)}
                    style={{ cursor: "pointer" }}
                    title="Click for details"
                  >
                    <td style={{ whiteSpace: "nowrap" }}>{new Date(e.created_at).toLocaleString()}</td>
                    <td>
                      {e.actor_user_id ? (
                        <button
                          type="button"
                          className="btn btn--link"
                          style={{ padding: 0, fontSize: "inherit" }}
                          title="Show all activity by this user — matches across renames"
                          onClick={(ev) => {
                            ev.stopPropagation();
                            setActorId({
                              id: e.actor_user_id!,
                              label: e.actor_name || e.actor_email || e.actor_user_id!,
                            });
                          }}
                        >
                          {e.actor_name || e.actor_email || "system"}
                        </button>
                      ) : (
                        e.actor_name || e.actor_email || "system"
                      )}
                    </td>
                    <td className="mono">{e.action}</td>
                    <td className="mono muted">
                      {[e.target_type, e.target_id].filter(Boolean).join(" / ") || "—"}
                    </td>
                    <td className="mono muted">{e.ip || "—"}</td>
                  </tr>
                  {expanded === e.id && (
                    <tr>
                      <td colSpan={5} style={{ background: "var(--surface-2)", padding: "8px 12px" }}>
                        <pre
                          className="mono"
                          style={{ margin: 0, fontSize: 12, whiteSpace: "pre-wrap", color: "var(--ink-2)" }}
                        >
                          {JSON.stringify(
                            {
                              id: e.id,
                              occurred_at: e.created_at,
                              actor: { id: e.actor_user_id, name: e.actor_name, email: e.actor_email },
                              action: e.action,
                              target: { type: e.target_type, id: e.target_id },
                              ip: e.ip,
                              metadata: e.metadata ?? {},
                            },
                            null,
                            2,
                          )}
                        </pre>
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
            </tbody>
          </table>
          {loadingMore && (
            <div className="placeholder" style={{ padding: "10px 12px", fontSize: 13 }}>
              Loading more…
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// ── License tab ────────────────────────────────────────────────────────

function LicenseTab() {
  const { status, loading } = useLicense();
  if (loading) return <div className="placeholder">Loading…</div>;
  return <LicensePanel status={status} />;
}

function LicensePanel({ status }: { status: LicenseStatus | null }) {
  const features: { key: keyof LicenseStatus["features"]; label: string }[] = [
    { key: "sso", label: "Single sign-on (OIDC)" },
    { key: "rbac_advanced", label: "Advanced RBAC (group access policies)" },
    { key: "audit_log", label: "Audit log" },
    { key: "retention_long", label: "Long retention" },
  ];
  const licensed = status?.licensed ?? false;
  return (
    <div style={{ maxWidth: 640 }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10, marginBottom: 12 }}>
        <h2 style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>License</h2>
        <span
          className="mono"
          style={{
            fontSize: 11,
            fontWeight: 700,
            padding: "2px 8px",
            borderRadius: 999,
            color: licensed ? "var(--ok, #3fb950)" : "var(--muted)",
            border: `1px solid ${licensed ? "var(--ok, #3fb950)" : "var(--border)"}`,
          }}
        >
          {licensed ? "ENTERPRISE" : "COMMUNITY"}
        </span>
      </div>

      {status?.warning && (
        <div className="alert alert--warn" style={{ marginBottom: 14 }}>
          {status.warning}
        </div>
      )}

      {licensed ? (
        <div className="card" style={{ padding: 16, marginBottom: 16 }}>
          <Field label="Customer" value={status?.customer || "—"} />
          <Field label="Plan" value={status?.plan || "—"} />
          <Field
            label="Expires"
            value={status?.expires_at ? new Date(status.expires_at).toLocaleDateString() : "Perpetual"}
          />
          <Field label="License ID" value={status?.license_id || "—"} mono />
        </div>
      ) : (
        <p className="muted" style={{ fontSize: 13.5, lineHeight: 1.6 }}>
          This install is running the free <strong>Community</strong> edition.
          Enterprise features below are disabled until a valid license key is set.
        </p>
      )}

      <h3 style={{ fontSize: 13, fontWeight: 600, margin: "8px 0 8px" }}>Enterprise features</h3>
      <div className="card" style={{ padding: 4, marginBottom: 16 }}>
        {features.map((f) => {
          const on = status?.features?.[f.key] ?? false;
          return (
            <div
              key={f.key}
              style={{
                display: "flex",
                alignItems: "center",
                justifyContent: "space-between",
                padding: "9px 12px",
                borderBottom: "1px solid var(--border)",
              }}
            >
              <span style={{ fontSize: 13 }}>{f.label}</span>
              <span
                className="mono"
                style={{ fontSize: 12, color: on ? "var(--ok, #3fb950)" : "var(--muted)" }}
              >
                {on ? "✓ enabled" : "locked"}
              </span>
            </div>
          );
        })}
      </div>

      <div className="card" style={{ padding: 16 }}>
        <h3 style={{ fontSize: 13, fontWeight: 600, margin: "0 0 8px" }}>Setting a license key</h3>
        <p className="muted" style={{ fontSize: 13, lineHeight: 1.6, margin: "0 0 8px" }}>
          The cell-api reads the key at startup from an environment variable —
          it's verified offline (no phone-home), so it works air-gapped:
        </p>
        <pre className="mono" style={{ fontSize: 12, margin: 0, whiteSpace: "pre-wrap" }}>
{`SLUICIO_LICENSE_KEY="sluicio_lic_…"
# or, point at a file:
SLUICIO_LICENSE_FILE=/etc/sluicio/license.key`}
        </pre>
        <p className="muted" style={{ fontSize: 12.5, lineHeight: 1.6, margin: "8px 0 0" }}>
          Contact ROMA IT AB for a license key. Restart the cell-api after setting it.
        </p>
      </div>
    </div>
  );
}

function Field({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div style={{ display: "flex", justifyContent: "space-between", padding: "5px 0", gap: 16 }}>
      <span className="muted" style={{ fontSize: 12.5 }}>{label}</span>
      <span className={mono ? "mono" : undefined} style={{ fontSize: 13, textAlign: "right" }}>
        {value}
      </span>
    </div>
  );
}
