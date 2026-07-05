// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// activeOrg — the client side of multi-org sessions. The cell-api
// resolves the active org per request from the X-Sluicio-Org header
// (slug or UUID), falling back to the user's first membership (see
// services/cell-api/internal/api/middleware/auth.go). We persist the
// user's chosen org here and the API client stamps it onto every
// request; no server-side session state is involved.

const KEY = "sluicio.active-org";

// getActiveOrgSlug returns the pinned org slug, or null when the user
// has never switched (server default: first membership).
export function getActiveOrgSlug(): string | null {
  try {
    return window.localStorage.getItem(KEY);
  } catch {
    // Storage can be unavailable (private mode, blocked cookies).
    // Behave as if unpinned.
    return null;
  }
}

export function setActiveOrgSlug(slug: string | null): void {
  try {
    if (slug) window.localStorage.setItem(KEY, slug);
    else window.localStorage.removeItem(KEY);
  } catch {
    /* ignore — unpinned behaviour still works */
  }
}

// switchToOrg pins the org and hard-navigates to the dashboard. A full
// reload (not a client-side route) is deliberate: every mounted page
// holds data fetched under the previous org, and the user's role —
// and with it whole nav sections — can differ in the new one.
export function switchToOrg(slug: string): void {
  setActiveOrgSlug(slug);
  window.location.assign("/");
}
