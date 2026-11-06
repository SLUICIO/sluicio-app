// SPDX-License-Identifier: FSL-1.1-Apache-2.0
import { describe, expect, it } from "vitest";
import { visibilityLabel } from "./viewVisibility";

describe("visibilityLabel", () => {
  it("names the team of a team view", () => {
    expect(visibilityLabel({ visibility: "team", groupName: "Orders" })).toBe("team · Orders");
  });
  it("falls back to team when the name is missing", () => {
    expect(visibilityLabel({ visibility: "team" })).toBe("team");
  });
  it("says private and org-wide", () => {
    expect(visibilityLabel({ visibility: "private" })).toBe("private");
    expect(visibilityLabel({ visibility: "org" })).toBe("org-wide");
  });
});
