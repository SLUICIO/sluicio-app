// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, expect, it } from "vitest";

import { canSeeSettingsTab, canSeeSystemSubtab } from "./settingsAccess";

const operator = { isOperator: true, managed: false, isAdmin: true };
const selfHostedAdmin = { isOperator: false, managed: false, isAdmin: true };
const managedAdmin = { isOperator: false, managed: true, isAdmin: true };
const managedMember = { isOperator: false, managed: true, isAdmin: false };

describe("canSeeSettingsTab", () => {
  it("shows organization tabs to everyone", () => {
    expect(canSeeSettingsTab("members", managedMember)).toBe(true);
  });
  it("keeps instance tabs with the operator on a self-hosted instance", () => {
    expect(canSeeSettingsTab("system", operator)).toBe(true);
    expect(canSeeSettingsTab("system", selfHostedAdmin)).toBe(false);
  });
  it("gives instance tabs to the org admin on a managed instance", () => {
    for (const tab of ["system", "retention", "license"]) {
      expect(canSeeSettingsTab(tab, managedAdmin)).toBe(true);
      expect(canSeeSettingsTab(tab, managedMember)).toBe(false);
    }
  });
});

describe("canSeeSystemSubtab", () => {
  it("hides announcements on a managed instance only", () => {
    expect(canSeeSystemSubtab("announcements", managedAdmin)).toBe(false);
    expect(canSeeSystemSubtab("announcements", operator)).toBe(true);
    expect(canSeeSystemSubtab("environments", managedAdmin)).toBe(true);
  });
});
