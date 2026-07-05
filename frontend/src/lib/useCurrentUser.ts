// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// useCurrentUser — the single source of truth for "who is this and
// what may they do?" everywhere in the UI.
//
// Real data comes from the UserProvider which fetches /api/v1/me on
// mount. On a 401 the provider renders the Login page instead of
// the children, so any component calling useCurrentUser() is
// guaranteed to be inside an authenticated session.
//
// Permission model: roles carry permissions, users don't. The mapping
// from RoleSlug → Permission[] lives in ROLE_PERMISSIONS below; the
// adapter in UserProvider.tsx converts the cell-api's admin / editor
// / viewer roles into the slugs this file uses. UI gates here are
// convenience — the cell-api enforces the same checks.

import { useCallback, useContext, useMemo } from "react";
import type {
  Organization,
  OrganizationMembership,
  Permission,
  Role,
  RoleSlug,
  User,
} from "../api/types";
import { AuthContext } from "../components/UserProvider";

// ── Role → permissions mapping ─────────────────────────────────────
//
// org-admin is a superset; the other roles are narrowly scoped. Keep
// the lists explicit rather than computed so a quick read of this
// file tells you exactly what each role can do.

const ROLE_PERMISSIONS: Record<RoleSlug, Permission[]> = {
  "org-admin": [
    "integration.read",
    "integration.write",
    "integration.delete",
    "stuck.replay",
    "alert.mute",
    "org.manage",
  ],
  "integration-contributor": [
    "integration.read",
    "integration.write",
    "integration.delete",
    "stuck.replay",
    "alert.mute",
  ],
  operator: [
    "integration.read",
    "stuck.replay",
    "alert.mute",
  ],
  viewer: [
    "integration.read",
  ],
};

const ROLE_DISPLAY: Record<RoleSlug, string> = {
  "org-admin": "Org admin",
  "integration-contributor": "Integration contributor",
  operator: "Operator",
  viewer: "Viewer",
};

export function displayRole(slug: RoleSlug): string {
  return ROLE_DISPLAY[slug];
}

// ── Hook ───────────────────────────────────────────────────────────

export interface CurrentUserContext {
  user: User;
  organization: Organization;
  roles: Role[];
  memberships: OrganizationMembership[];
  /** True iff any of the user's roles in the active org grants `perm`. */
  can: (perm: Permission) => boolean;
  /** True iff this user is a cell operator (gates the Operator surface). */
  isOperator: boolean;
  /** Sign the user out — POST /api/v1/auth/logout and drop session state. */
  signOut: () => void;
}

export function useCurrentUser(): CurrentUserContext {
  const ctx = useContext(AuthContext);
  if (!ctx || !ctx.data) {
    // Outside the provider, or the provider is still loading. This
    // shouldn't happen in normal app flow — UserProvider only renders
    // children once /me has resolved — but we keep the hook robust so
    // a stray dev-mode render during HMR doesn't crash the page.
    throw new Error("useCurrentUser must be used inside <UserProvider>");
  }
  const data = ctx.data;

  const { user, organization, roles, memberships } = useMemo(() => {
    const memberships = withSyntheticEmpty(data.user);
    const active =
      memberships.find((m) => m.organization.id === data.active_organization_id) ??
      memberships[0];
    return {
      user: data.user,
      organization: active.organization,
      roles: active.roles,
      memberships,
    };
  }, [data]);

  const can = useCallback(
    (perm: Permission): boolean => {
      for (const role of roles) {
        if (ROLE_PERMISSIONS[role.slug]?.includes(perm)) return true;
      }
      return false;
    },
    [roles],
  );

  const signOut = useCallback(() => {
    const ok = window.confirm("Sign out of Sluicio?");
    if (!ok) return;
    void ctx.signOut();
  }, [ctx]);

  return { user, organization, roles, memberships, can, isOperator: user.isOperator, signOut };
}

function withSyntheticEmpty(u: User): OrganizationMembership[] {
  // Defensive: callers should always get at least one membership so
  // useCurrentUser can resolve an active org. If a user with zero
  // memberships ever shows up, surface a synthetic "no organization"
  // entry so the UI renders a sensible empty state rather than crash.
  if (u.memberships.length > 0) return u.memberships;
  return [
    {
      organization: { id: "org_none", slug: "none", name: "No organization" },
      roles: [{ slug: "viewer", name: ROLE_DISPLAY.viewer }],
    },
  ];
}
