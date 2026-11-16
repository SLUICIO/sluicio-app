// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Two services in one flow usually share a long prefix -
// product-consolidation-aggregator-service beside
// product-consolidation-extractor-service - and a node that cuts the
// name at its edge renders both as "product-consolidati…". The graph
// then stops distinguishing the things it exists to distinguish, which
// is how this was reported.

import { describe, expect, it } from "vitest";
import { maxNameLines } from "./IntegrationFlow";

describe("maxNameLines", () => {
  it("keeps short names on one line, so a simple flow stays compact", () => {
    expect(maxNameLines(["checkout-api", "erp-adapter"])).toBe(1);
  });

  it("gives the reported names a second line", () => {
    expect(
      maxNameLines([
        "product-consolidation-aggregator-service",
        "product-consolidation-extractor-service",
      ]),
    ).toBe(2);
  });

  it("is decided by the longest name, since every node is the same height", () => {
    expect(maxNameLines(["a", "product-consolidation-aggregator-service"])).toBe(2);
  });

  it("stops at two lines: a node has to stay a node", () => {
    expect(maxNameLines(["x".repeat(400)])).toBe(2);
  });

  it("handles an empty flow without asking for half a line", () => {
    expect(maxNameLines([])).toBe(1);
  });

  // 8px a character is about 14px semibold in the app's font; the node
  // has 152px for the name.
  const measured = (t: string) => t.length * 8;

  it("breaks after a hyphen the way the browser does", () => {
    // 20 characters, 160px: the browser breaks after "part-", which a
    // count of 24 characters a line missed and the trace count spilled
    // out of the node.
    expect(maxNameLines(["wms-loader-part-sync"], measured)).toBe(2);
    expect(maxNameLines(["erp-loader-sync"], measured)).toBe(1);
  });

  it("breaks a part wider than a line anywhere", () => {
    expect(maxNameLines(["a-" + "x".repeat(25)], measured)).toBe(2);
  });
});
