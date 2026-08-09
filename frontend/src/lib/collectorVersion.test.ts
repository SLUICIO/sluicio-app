// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// These mirror the Go tests in internal/collectorversion on purpose. If
// the two implementations ever disagree, the server believes a snippet
// is correct while the user's collector refuses it, and the user has no
// way to tell which of us is wrong.

import { describe, expect, it } from "vitest";
import { isValidVersion, versionAtLeast } from "./collectorVersion";

describe("versionAtLeast", () => {
  it("compares numerically, not as strings", () => {
    // The whole reason this function exists: "0.9.0" > "0.146.0" as
    // strings, and v0.146.0 is where the exporter rename lives.
    expect(versionAtLeast("0.9.0", "0.146.0")).toBe(false);
    expect(versionAtLeast("0.146.0", "0.9.0")).toBe(true);
  });

  it("treats a version as at least itself", () => {
    expect(versionAtLeast("0.146.0", "0.146.0")).toBe(true);
  });

  it("holds the rename boundary from both sides", () => {
    expect(versionAtLeast("0.145.9", "0.146.0")).toBe(false);
    expect(versionAtLeast("0.146.0", "0.146.0")).toBe(true);
    expect(versionAtLeast("0.157.0", "0.146.0")).toBe(true);
  });

  it("tolerates a leading v, because people paste it", () => {
    expect(versionAtLeast("v0.157.0", "0.146.0")).toBe(true);
  });

  it("accepts a two-part version", () => {
    expect(versionAtLeast("0.146", "0.146.0")).toBe(true);
  });

  it("does NOT treat an unreadable version as new", () => {
    // Guessing "recent" would emit the newest syntax to the one customer
    // whose setting we failed to parse.
    expect(versionAtLeast("latest", "0.1.0")).toBe(false);
    expect(versionAtLeast("", "0.1.0")).toBe(false);
    expect(versionAtLeast("0.x.0", "0.1.0")).toBe(false);
  });

  it("rejects a negative or fractional component", () => {
    expect(versionAtLeast("0.-1.0", "0.1.0")).toBe(false);
    expect(versionAtLeast("0.1.5.2", "0.1.0")).toBe(true); // extra parts ignored
    expect(versionAtLeast("0.1.x", "0.1.0")).toBe(false);
  });
});

describe("isValidVersion", () => {
  it("accepts what a customer would type", () => {
    expect(isValidVersion("0.157.0")).toBe(true);
    expect(isValidVersion("v0.157.0")).toBe(true);
    expect(isValidVersion("0.157")).toBe(true);
  });

  it("rejects words", () => {
    expect(isValidVersion("latest")).toBe(false);
    expect(isValidVersion("newest supported")).toBe(false);
  });
});
