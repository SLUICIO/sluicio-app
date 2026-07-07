// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Operator — the cell super-admin surface, above the org roles. Only
// reachable by a user flagged is_operator (the route is guarded, and the
// nav link is hidden otherwise). Three concerns:
//
//   Organizations — create / rename / delete orgs and assign members.
//   Operators     — promote / demote other users to cell operator.
//   Cell settings — deep links to the (now operator-gated) SMTP /
//                   retention / license panels.
//
// Every call here hits an operator-gated endpoint; the cell-api enforces
// the same, so a non-operator who forces their way to /operator gets 403s.

import { Fragment, useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import type { AuthRole, MemberRow, OperatorOrg, OperatorUser } from "../api/types";
import AnnouncementsAdmin from "../components/AnnouncementsAdmin";
import { usePageTitle } from "../lib/usePageTitle";

const ROLES: AuthRole[] = ["admin", "editor", "viewer"];
const slugRe = /^[a-z0-9-]{1,64}$/;

export default function Operator() {
  usePageTitle("Cell operator");
  return (
    <div>
      <div className="page__header">
        <div>
          <h1 className="page__title">Cell operator</h1>
          <p className="page__subtitle">
            Manage organizations, members, and cell-wide settings shared across
            every org on this cell.
          </p>
        </div>
      </div>
      <OrganizationsCard />
      <OperatorsCard />
      <CellSettingsCard />
      <AnnouncementsAdmin scope="cell" />
    </div>
  );
}

// ── Organizations ──────────────────────────────────────────────────

function OrganizationsCard() {
  const [orgs, setOrgs] = useState<OperatorOrg[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [busy, setBusy] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);

  const reload = useCallback(() => {
    api
      .operatorListOrgs()
      .then((r) => setOrgs(r.orgs ?? []))
      .catch((e) => setError(String(e.message ?? e)));
  }, []);
  useEffect(() => reload(), [reload]);

  const create = async () => {
    setError(null);
    if (!name.trim()) {
      setError("Name is required.");
      return;
    }
    if (!slugRe.test(slug.trim())) {
      setError("Slug must be lowercase letters, digits, and hyphens (1–64 chars).");
      return;
    }
    setBusy(true);
    try {
      await api.operatorCreateOrg({ name: name.trim(), slug: slug.trim() });
      setName("");
      setSlug("");
      reload();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const rename = async (o: OperatorOrg) => {
    const nn = window.prompt("Organization name:", o.name);
    if (nn === null) return;
    const ns = window.prompt("Slug (lowercase, digits, hyphens):", o.slug);
    if (ns === null) return;
    if (ns.trim() && !slugRe.test(ns.trim())) {
      window.alert("Invalid slug.");
      return;
    }
    try {
      await api.operatorUpdateOrg(o.id, { name: nn.trim(), slug: ns.trim() });
      reload();
    } catch (e) {
      window.alert(String((e as Error).message ?? e));
    }
  };

  const remove = async (o: OperatorOrg) => {
    if (
      !window.confirm(
        `Delete organization "${o.name}"? This cascades through its members, groups, service accounts, and tokens. This cannot be undone.`,
      )
    )
      return;
    try {
      await api.operatorDeleteOrg(o.id);
      if (expanded === o.id) setExpanded(null);
      reload();
    } catch (e) {
      window.alert(String((e as Error).message ?? e));
    }
  };

  return (
    <div className="card" style={{ marginBottom: 16, padding: 16 }}>
      <h2 style={{ fontSize: 16, fontWeight: 600, margin: "0 0 12px" }}>Organizations</h2>
      {error && (
        <div className="alert alert--error" style={{ marginBottom: 12 }}>
          {error}
        </div>
      )}

      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap", marginBottom: 16 }}>
        <input
          className="svc-input"
          placeholder="Organization name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          style={{ minWidth: 200 }}
        />
        <input
          className="svc-input"
          placeholder="slug"
          value={slug}
          onChange={(e) => setSlug(e.target.value.toLowerCase())}
          style={{ minWidth: 160 }}
        />
        <button className="btn primary" onClick={create} disabled={busy}>
          {busy ? "Creating…" : "Create organization"}
        </button>
      </div>

      {orgs === null ? (
        <div className="placeholder">Loading…</div>
      ) : orgs.length === 0 ? (
        <div className="placeholder">No organizations.</div>
      ) : (
        <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}>
          <thead>
            <tr style={{ textAlign: "left", color: "var(--muted)" }}>
              <th style={{ padding: "6px 8px" }}>Name</th>
              <th style={{ padding: "6px 8px" }}>Slug</th>
              <th style={{ padding: "6px 8px" }}>Members</th>
              <th style={{ padding: "6px 8px" }}></th>
            </tr>
          </thead>
          <tbody>
            {orgs.map((o) => (
              <Fragment key={o.id}>
                <tr style={{ borderTop: "1px solid var(--border)" }}>
                  <td style={{ padding: "8px" }}>{o.name}</td>
                  <td style={{ padding: "8px" }}>
                    <code className="mono">{o.slug}</code>
                  </td>
                  <td style={{ padding: "8px" }}>{o.member_count}</td>
                  <td style={{ padding: "8px", textAlign: "right", whiteSpace: "nowrap" }}>
                    <button
                      className="btn btn--sm"
                      onClick={() => setExpanded(expanded === o.id ? null : o.id)}
                    >
                      {expanded === o.id ? "Hide members" : "Members"}
                    </button>{" "}
                    <button className="btn btn--sm" onClick={() => rename(o)}>
                      Rename
                    </button>{" "}
                    <button className="btn btn--sm btn--danger" onClick={() => remove(o)}>
                      Delete
                    </button>
                  </td>
                </tr>
                {expanded === o.id && (
                  <tr>
                    <td colSpan={4} style={{ padding: "0 8px 12px", background: "var(--surface-2)" }}>
                      <OrgMembers orgId={o.id} onChanged={reload} />
                    </td>
                  </tr>
                )}
              </Fragment>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

// ── Per-org member management (expanded row) ───────────────────────

function OrgMembers({ orgId, onChanged }: { orgId: string; onChanged: () => void }) {
  const [members, setMembers] = useState<MemberRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<AuthRole>("viewer");
  const [busy, setBusy] = useState(false);

  const reload = useCallback(() => {
    api
      .operatorListOrgMembers(orgId)
      .then((r) => setMembers(r.members ?? []))
      .catch((e) => setError(String(e.message ?? e)));
  }, [orgId]);
  useEffect(() => reload(), [reload]);

  const add = async () => {
    setError(null);
    if (!email.trim()) {
      setError("Email is required.");
      return;
    }
    setBusy(true);
    try {
      await api.operatorAddOrgMember(orgId, {
        email: email.trim(),
        role,
        password: password.trim() || undefined,
      });
      setEmail("");
      setPassword("");
      setRole("viewer");
      reload();
      onChanged();
    } catch (e) {
      setError(String((e as Error).message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const changeRole = async (userId: string, next: AuthRole) => {
    try {
      await api.operatorUpdateOrgMemberRole(orgId, userId, next);
      reload();
    } catch (e) {
      window.alert(String((e as Error).message ?? e));
    }
  };

  const removeMember = async (m: MemberRow) => {
    if (!window.confirm(`Remove ${m.user.email} from this organization?`)) return;
    try {
      await api.operatorRemoveOrgMember(orgId, m.user.id);
      reload();
      onChanged();
    } catch (e) {
      window.alert(String((e as Error).message ?? e));
    }
  };

  return (
    <div style={{ padding: "10px 4px" }}>
      {error && (
        <div className="alert alert--error" style={{ marginBottom: 8 }}>
          {error}
        </div>
      )}
      {members === null ? (
        <div className="muted" style={{ fontSize: 12 }}>
          Loading members…
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          {members.length === 0 && (
            <div className="muted" style={{ fontSize: 12 }}>
              No members yet.
            </div>
          )}
          {members.map((m) => (
            <div key={m.user.id} style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 13 }}>
              <span style={{ flex: 1 }}>{m.user.email}</span>
              <select
                className="svc-input"
                value={m.role}
                onChange={(e) => changeRole(m.user.id, e.target.value as AuthRole)}
                style={{ minWidth: 110 }}
              >
                {ROLES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
              <button className="btn btn--sm" onClick={() => removeMember(m)}>
                Remove
              </button>
            </div>
          ))}
        </div>
      )}

      <div style={{ display: "flex", gap: 8, alignItems: "center", flexWrap: "wrap", marginTop: 12 }}>
        <input
          className="svc-input"
          placeholder="email to add"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          style={{ minWidth: 200 }}
        />
        <select
          className="svc-input"
          value={role}
          onChange={(e) => setRole(e.target.value as AuthRole)}
          style={{ minWidth: 110 }}
        >
          {ROLES.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
        <input
          className="svc-input"
          type="password"
          placeholder="initial password (optional — SSO users omit)"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          style={{ minWidth: 240 }}
        />
        <button className="btn primary" onClick={add} disabled={busy}>
          Add member
        </button>
      </div>
    </div>
  );
}

// ── Operators ──────────────────────────────────────────────────────

const USERS_PAGE = 50;

function OperatorsCard() {
  const [users, setUsers] = useState<OperatorUser[] | null>(null);
  const [total, setTotal] = useState(0);
  const [query, setQuery] = useState("");
  const [offset, setOffset] = useState(0);
  const [error, setError] = useState<string | null>(null);

  const reload = useCallback(() => {
    api
      .operatorListUsers(query, offset, USERS_PAGE)
      .then((r) => {
        setUsers(r.users ?? []);
        setTotal(r.total ?? (r.users ?? []).length);
      })
      .catch((e) => setError(String(e.message ?? e)));
  }, [query, offset]);
  // Debounce keystrokes; paging reloads immediately via the offset dep.
  useEffect(() => {
    const t = setTimeout(reload, query ? 250 : 0);
    return () => clearTimeout(t);
  }, [reload, query]);

  const toggle = async (u: OperatorUser, next: boolean) => {
    if (!next && !window.confirm(`Remove operator access from ${u.email}?`)) return;
    try {
      await api.operatorSetUserOperator(u.id, next);
      reload();
    } catch (e) {
      window.alert(String((e as Error).message ?? e));
    }
  };

  const toggleDemo = async (u: OperatorUser) => {
    const next = !u.is_demo;
    if (
      next &&
      !window.confirm(
        `Mark ${u.email} as a shared demo account? Profile, password, MFA and token self-service will be disabled for it.`,
      )
    )
      return;
    try {
      await api.operatorSetUserDemo(u.id, next);
      reload();
    } catch (e) {
      window.alert(String((e as Error).message ?? e));
    }
  };

  return (
    <div className="card" style={{ marginBottom: 16, padding: 16 }}>
      <h2 style={{ fontSize: 16, fontWeight: 600, margin: "0 0 4px" }}>Operators</h2>
      <p className="muted" style={{ fontSize: 12, marginTop: 0, marginBottom: 12 }}>
        Operators manage organizations and cell-wide settings. The cell always
        keeps at least one operator — the last can't be demoted.
      </p>
      {error && (
        <div className="alert alert--error" style={{ marginBottom: 12 }}>
          {error}
        </div>
      )}
      <input
        className="input"
        type="search"
        placeholder="Search by email or name…"
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          setOffset(0);
        }}
        style={{ marginBottom: 10, maxWidth: 320 }}
      />
      {users === null ? (
        <div className="placeholder">Loading…</div>
      ) : users.length === 0 ? (
        <div className="placeholder">No users match.</div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          {users.map((u) => (
            <label
              key={u.id}
              style={{ display: "flex", alignItems: "center", gap: 10, fontSize: 13, padding: "4px 0" }}
            >
              <input
                type="checkbox"
                checked={u.is_operator ?? false}
                onChange={(e) => toggle(u, e.target.checked)}
              />
              <span style={{ flex: 1 }}>
                {u.email}
                {u.name && u.name !== u.email ? <span className="muted"> · {u.name}</span> : null}
              </span>
              {u.is_operator ? <span className="badge-brand">operator</span> : null}
              {u.is_demo ? <span className="pill">demo</span> : null}
              <button
                type="button"
                className="btn btn--link"
                style={{ fontSize: 12 }}
                onClick={(e) => {
                  e.preventDefault();
                  void toggleDemo(u);
                }}
                title="Demo accounts keep their product access but self-service (profile, password, MFA, tokens) is disabled"
              >
                {u.is_demo ? "unmark demo" : "mark demo"}
              </button>
            </label>
          ))}
        </div>
      )}
      {total > USERS_PAGE && (
        <div style={{ display: "flex", alignItems: "center", gap: 10, marginTop: 12 }}>
          <button
            type="button"
            className="btn btn--sm"
            disabled={offset === 0}
            onClick={() => setOffset(Math.max(0, offset - USERS_PAGE))}
          >
            ← Previous
          </button>
          <span className="muted" style={{ fontSize: 12 }}>
            {offset + 1}–{Math.min(offset + USERS_PAGE, total)} of {total}
          </span>
          <button
            type="button"
            className="btn btn--sm"
            disabled={offset + USERS_PAGE >= total}
            onClick={() => setOffset(offset + USERS_PAGE)}
          >
            Next →
          </button>
        </div>
      )}
    </div>
  );
}

// ── Cell-wide settings (links) ─────────────────────────────────────

function CellSettingsCard() {
  return (
    <div className="card" style={{ marginBottom: 16, padding: 16 }}>
      <h2 style={{ fontSize: 16, fontWeight: 600, margin: "0 0 4px" }}>Cell-wide settings</h2>
      <p className="muted" style={{ fontSize: 12, marginTop: 0, marginBottom: 12 }}>
        These apply to every organization on the cell, so only operators can
        change them.
      </p>
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        <a className="btn btn--sm" href="/settings?tab=system">
          Email (SMTP) &amp; security
        </a>
        <a className="btn btn--sm" href="/settings?tab=retention">
          Telemetry retention
        </a>
        <a className="btn btn--sm" href="/settings?tab=license">
          License
        </a>
      </div>
    </div>
  );
}
