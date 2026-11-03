// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Who sees the instance-wide parts of Settings. They are the operator's, but
// a managed instance has no operator, so there they go to the org's admin:
// otherwise the Environments, Security and template settings are out of reach
// for everyone in the instance. The server decides what a write may change;
// values the deployment owns show read-only.

export interface SettingsViewer {
  isOperator: boolean;
  managed: boolean;
  isAdmin: boolean;
}

const INSTANCE_TABS = ["retention", "system", "license"];

export function canSeeSettingsTab(tab: string, viewer: SettingsViewer): boolean {
  if (!INSTANCE_TABS.includes(tab)) return true;
  return viewer.isOperator || (viewer.managed && viewer.isAdmin);
}

// Announcements reach everyone on the instance and belong to whoever runs it,
// so they stay hidden on a managed instance. Their routes refuse there too.
export function canSeeSystemSubtab(sub: string, viewer: SettingsViewer): boolean {
  return sub !== "announcements" || !viewer.managed;
}
