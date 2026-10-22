// SPDX-License-Identifier: FSL-1.1-Apache-2.0

import { describe, expect, it } from "vitest";
import type { Dashboard, Integration } from "../api/types";
import { matchesIncludeRule, ruleIntegrations } from "./dashboardItems";

const integ = (id: string, country?: string, tag?: string): Integration =>
  ({
    id,
    name: id,
    slug: id,
    organization_id: "o",
    description: "",
    created_at: "",
    updated_at: "",
    metadata_values: country ? { country } : {},
    tags: tag ? [{ id: tag, slug: tag, name: tag }] : [],
  }) as unknown as Integration;

describe("dashboard include rule", () => {
  const se = integ("se", "Sweden", "payments");
  const de = integ("de", "Germany");
  it("matches a metadata value or a tag", () => {
    expect(matchesIncludeRule(se, { kind: "metadata", key: "country", value: "Sweden" })).toBe(true);
    expect(matchesIncludeRule(de, { kind: "metadata", key: "country", value: "Sweden" })).toBe(false);
    expect(matchesIncludeRule(se, { kind: "tag", value: "payments" })).toBe(true);
    expect(matchesIncludeRule(de, { kind: "tag", value: "payments" })).toBe(false);
    expect(matchesIncludeRule(de, null)).toBe(true);
  });

  it("narrows only an auto-include dashboard", () => {
    const d = { autoIncludeAll: true, includeRule: { kind: "metadata", key: "country", value: "Sweden" } } as Dashboard;
    expect(ruleIntegrations(d, [se, de]).map((i) => i.id)).toEqual(["se"]);
    expect(ruleIntegrations({ ...d, autoIncludeAll: false }, [se, de]).length).toBe(2);
  });
});
