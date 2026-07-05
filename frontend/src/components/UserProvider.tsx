// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// UserProvider — the SPA's auth gate. Fetches /api/v1/me once on
// mount; if the cell-api returns 401 the user sees the Login page
// instead of the app. After a successful login the provider re-
// fetches /me so the rest of the app renders with the new context.
//
// Inside this provider the adapter `fromMe` converts the backend's
// shape (admin / editor / viewer roles; `org` vs `organization`)
// into the historical CurrentUserResponse shape that `useCurrentUser`
// already consumes — so existing call sites like `can("integration.
// write")` keep working without rewriting every component.

import { createContext, ReactNode, useCallback, useEffect, useState } from "react";
import { api } from "../api/client";
import { getActiveOrgSlug, setActiveOrgSlug } from "../lib/activeOrg";
import type {
  AuthMembership,
  AuthRole,
  CurrentUserResponse,
  MeResponse,
  Organization,
  OrganizationMembership,
  RoleSlug,
  User,
} from "../api/types";
import Login from "../pages/Login";
import ResetPassword from "../pages/ResetPassword";

// Shape exposed via context. `null` while we're still fetching /me
// for the first time; otherwise a populated response.
export interface AuthContextValue {
  data: CurrentUserResponse | null;
  refetch: () => Promise<void>;
  signOut: () => Promise<void>;
}

export const AuthContext = createContext<AuthContextValue | null>(null);

interface Props {
  children: ReactNode;
}

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; data: CurrentUserResponse }
  | { kind: "logged-out" }
  | { kind: "error"; message: string };

export default function UserProvider({ children }: Props) {
  const [state, setState] = useState<LoadState>({ kind: "loading" });

  const fetchMe = useCallback(async () => {
    try {
      const me = await api.me();
      setState({ kind: "ready", data: fromMe(me) });
    } catch (e) {
      const msg = String((e as Error).message ?? e);
      // The API client throws `Error("<status> <message>")`; check
      // the leading status digit to discriminate "not logged in"
      // from "actually broken."
      if (msg.startsWith("401")) {
        // A pinned org the user can no longer access (membership
        // revoked, org deleted, or a different user signed in on this
        // browser) also 401s — on every call, including /me. Drop the
        // pin and try once more before concluding "logged out".
        if (getActiveOrgSlug() !== null) {
          setActiveOrgSlug(null);
          try {
            const me = await api.me();
            setState({ kind: "ready", data: fromMe(me) });
            return;
          } catch {
            /* fall through to logged-out */
          }
        }
        setState({ kind: "logged-out" });
      } else {
        setState({ kind: "error", message: msg });
      }
    }
  }, []);

  useEffect(() => {
    void fetchMe();
  }, [fetchMe]);

  const signOut = useCallback(async () => {
    try {
      await api.logout();
    } catch {
      // Even if the API call fails, drop our local state — the
      // browser is about to be re-asked /me anyway and the cell-api
      // will tell us authoritatively.
    }
    // The org pin is per-person, not per-browser: don't let it leak
    // into whoever signs in next on this machine.
    setActiveOrgSlug(null);
    setState({ kind: "logged-out" });
  }, []);

  if (state.kind === "loading") {
    return <FullPageMessage>Loading…</FullPageMessage>;
  }
  if (state.kind === "logged-out") {
    // The password-reset link lands here unauthenticated; render the reset
    // page instead of the sign-in form when the path matches.
    if (window.location.pathname === "/reset-password") {
      return <ResetPassword />;
    }
    return <Login onSuccess={fetchMe} />;
  }
  if (state.kind === "error") {
    return (
      <FullPageMessage>
        <div style={{ textAlign: "center" }}>
          <div style={{ fontSize: 18, fontWeight: 600, marginBottom: 8 }}>
            Couldn’t reach the cell-api
          </div>
          <div className="muted" style={{ fontSize: 13 }}>{state.message}</div>
          <button
            type="button"
            className="btn btn--primary"
            style={{ marginTop: 16 }}
            onClick={() => {
              setState({ kind: "loading" });
              void fetchMe();
            }}
          >
            Retry
          </button>
        </div>
      </FullPageMessage>
    );
  }

  return (
    <AuthContext.Provider value={{ data: state.data, refetch: fetchMe, signOut }}>
      {children}
    </AuthContext.Provider>
  );
}

// FullPageMessage renders centred content at app-shell size — used
// for the loading skeleton + the error fallback.
function FullPageMessage({ children }: { children: ReactNode }) {
  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        display: "grid",
        placeItems: "center",
        background: "var(--surface)",
        color: "var(--ink)",
      }}
    >
      {children}
    </div>
  );
}

// ── adapter: cell-api MeResponse → frontend CurrentUserResponse ────
//
// The historical SPA shape used:
//   - RoleSlug   ∈ org-admin | integration-contributor | operator | viewer
//   - membership.organization, membership.roles[]
//   - user.initials computed from name
// The cell-api uses:
//   - AuthRole   ∈ admin | editor | viewer
//   - membership.org, membership.role
// Mapping table:
//   admin   → org-admin
//   editor  → integration-contributor
//   viewer  → viewer
// Mapping is opinionated; if you add finer SPA roles later, this
// is the only place you need to widen.

function fromMe(me: MeResponse): CurrentUserResponse {
  const memberships: OrganizationMembership[] = me.memberships.map(adaptMembership);
  const user: User = {
    id: me.user.id,
    email: me.user.email,
    name: me.user.name,
    initials: initialsFor(me.user.name || me.user.email),
    isOperator: me.user.is_operator ?? false,
    isDemo: me.user.is_demo ?? false,
    memberships,
  };
  return {
    user,
    active_organization_id: me.principal.org_id || memberships[0]?.organization.id || "",
  };
}

function adaptMembership(m: AuthMembership): OrganizationMembership {
  const slug = mapRoleSlug(m.role);
  const org: Organization = {
    id: m.org.id,
    slug: m.org.slug,
    name: m.org.name,
  };
  return {
    organization: org,
    roles: [{ slug, name: displayForSlug(slug) }],
  };
}

function mapRoleSlug(role: AuthRole): RoleSlug {
  switch (role) {
    case "admin":
      return "org-admin";
    case "editor":
      return "integration-contributor";
    case "viewer":
      return "viewer";
  }
}

function displayForSlug(slug: RoleSlug): string {
  switch (slug) {
    case "org-admin":
      return "Admin";
    case "integration-contributor":
      return "Editor";
    case "operator":
      return "Operator";
    case "viewer":
      return "Viewer";
  }
}

function initialsFor(s: string): string {
  const trimmed = s.trim();
  if (!trimmed) return "?";
  // For "Robert Mayer" → "RM"; for "alice@example.com" → "A".
  const parts = trimmed.split(/[\s.@_-]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return parts[0].slice(0, 2).toUpperCase();
}
